---
title: "Alerts"
description: "Get a Slack message or an email when a build or deploy fails, a service is down, backups fail, a pod crashes, storage fills or a node stops responding."
---

Geass can tell you when something goes wrong, so that you do not have to be looking at the dashboard to find out. You set up one or more **channels**, each a Slack channel or a list of email addresses, and choose which alerts each one hears. From then on, a failed build, a deploy Geass gave up on, a service with nothing running, a database whose backups are failing, a pod that keeps crashing, a volume that is filling, a node that stopped responding or a cluster problem sends a message, and another one when the problem is gone.

Alerting is under **Settings** → **Alerts**, and only a signed-in admin can see or change it. It needs the [monitoring add-on](/docs/guides/observability/), which is on by default: the alerts are made by Prometheus and sent by Alertmanager, both of which it installs. Geass does not send them itself.

## What you are told about

| Alert | Sent when | Waits |
| --- | --- | --- |
| **Build failed** | A build of a repository service's current settings fails. A service that is built again and fails again is a new alert | None |
| **Deploy failed** | A service, function, database or bucket could not be deployed and Geass gave up: it failed five times in a row, or for three minutes, or for a reason no retry will fix. It stays open while you try again, and ends when the resource is ready | None |
| **Service down** | A service that was deployed has no ready replica. Not one scaled to zero, and not one already reported as failing to deploy | 5 minutes |
| **Backups failing** | A database's backups are failing, so its recent changes may not be recoverable. See [Backups and restore](/docs/guides/backups-and-restore/) | None |
| **Node not ready** | A node of the cluster is not ready, or one that Geass could not set up. Not a node whose K3s is being upgraded | 5 minutes |
| **Crash loop** | A container of a pod keeps crashing and being restarted. A pod of a project belongs to that project | 15 minutes |
| **Storage filling** | A project's volume is more than 85% full (warning, after 10 minutes) or more than 95% full (critical, after 5), a platform volume is more than 90% full (10 minutes), or a node's disk is more than 90% full (15 minutes) | As listed |
| **Cluster problem** | A cluster upgrade stopped, certificates cannot be issued, Geass gave up getting the cluster ready, the network plugin does not enforce network policies, or the operator itself is down or not keeping up (the alerts under [Metrics and alerts for the operator](/docs/guides/observability/#metrics-and-alerts-for-the-operator)) | 10 minutes for the cluster itself, and what each of the operator's alerts says |

Prometheus looks every minute, so a problem is announced about a minute after its wait ends, and Alertmanager then holds a group for **30 seconds** so that alerts that arrive together are one message. A node that goes down takes every service on it with it, and that is one message for the node and one for each project's services, not one for each.

A message is **critical** for a deploy that failed or a service that is down in a production environment, for a database's backups, for a node, for a project volume that is almost full, and for a cluster upgrade that stopped or a cluster that is not ready. It is **warning** for the rest. The severity colors the message. It does not route it: a channel hears the alerts you chose for it, whatever their severity.

### When a problem ends

A message that says a problem is **resolved** is sent when it has gone for **five minutes**, not at once. Prometheus keeps an alert firing that long after its cause has gone, because when the operator restarts, or another one takes over, it says nothing for a minute, and without the delay every problem still there would be announced as resolved and then announced again.

A problem that is still there is mentioned again every **twelve hours**.

### What is left out

- **Pull request environments.** A copy made for a pull request fails as a matter of course, and the pull request already says so through [GitHub](/docs/guides/github-and-builds/). Nothing in one is announced.
- **Drafts** that have not been deployed, and anything being deleted.

### When you add the first channel

Whatever is firing at that moment is announced to it, within a minute. That is how you find out about a problem that was already there, and it can be several messages if there are several. **Firing now** on the page lists what Alertmanager holds.

## Set up a Slack channel

1. In Slack, create an **incoming webhook** for the channel you want (the Slack app directory's *Incoming Webhooks*), and copy its URL. It starts with `https://hooks.slack.com/services/`.
2. In Geass, open **Settings** → **Alerts** → **Slack channel**. Give it a name, paste the URL, choose the alerts and the projects and environments it hears, and choose **Add channel**.
3. Choose **Send test** from the channel's menu. A test message arrives, or the page tells you why not.

The URL is a secret: anyone who has it can post to the channel. Geass keeps it in the Secret `geass-alert-channels` in `geass-system`, under the channel's id, and never sends it back to the browser. The page shows only the host it posts to. Anything that takes Slack's incoming webhook JSON works, such as Mattermost or Rocket.Chat.

A Slack URL that is changed is the one used from then on, after a minute or two: the monitoring add-on has to notice the change.

## Set up email

Email goes through a mail server of your own: your cloud's (Amazon SES, Postmark, Mailgun, SendGrid) or your company's. Geass does not send mail itself.

1. Open **Settings** → **Alerts** → **Set up mail server** and fill in:

   | Field | What to put |
   | --- | --- |
   | Server and port | Your provider's SMTP host and port, for example `email-smtp.eu-north-1.amazonaws.com` and `587` |
   | Security | **STARTTLS** with port 587, or **TLS from the start** with port **465**, which is the only port that is encrypted from the first byte. **None** has no encryption, so it cannot have a password: choose it only for a server that asks for none |
   | Username and password | The provider's SMTP credentials. Leave the username empty for a server that does not ask. The password is kept in the Secret `geass-alert-smtp` and is not sent back to the browser |
   | From | The address the mail comes from, such as `alerts@example.com` or `Geass <alerts@example.com>`. Most providers only accept an address or domain you have verified with them |

2. Choose **Test connection**. Geass, from the operator, connects, encrypts and signs in as a message would, and sends nothing. A wrong password or a closed port is reported with the server's own words. It does not show that the monitoring namespace can reach the server too: **Send test** does.
3. Choose **Email** and give the channel a name and up to 20 addresses, separated by commas, spaces or lines.
4. Choose **Send test** from the channel's menu.

The mail is plain text and HTML, with `Auto-Submitted: auto-generated` in its header so that out-of-office replies are not sent back. Alertmanager checks the mail server's certificate and there is no way to skip it.

## Choose what a channel hears

Each channel has its own filters:

| Filter | Meaning |
| --- | --- |
| **Alerts** | Which of the kinds above. Choosing all of them also covers kinds added later |
| **Projects** | Limit the channel to these projects. Leave all unchecked for every project |
| **Environments** | Limit the channel to these environments, such as `production`. Leave all unchecked for every environment |
| **Also tell me when a problem is resolved** | On by default. A channel for a pager can turn it off |
| **Paused** | Keeps the channel and sends nothing |

The alerts that belong to the cluster itself (nodes, certificates, upgrades, the operator, a platform volume or a pod outside the projects) have no project. They go only to channels that are **not limited to projects**, so that a channel made for one team's project does not hear about the cluster's nodes. The environment filter narrows alerts about a service or a database and leaves the cluster's alone, so a channel for production still hears that a node went down.

A typical setup is two channels: a Slack channel with everything, and an email channel for whoever is on call, limited to the **production** environment and without **Build failed**.

## What a message looks like

A Slack message has a colored bar (red for critical, amber for warning, green for resolved), a title such as *Deploy failed* with a count when there are several, a line for each thing (`web (Morning Beach / production)`), what it means and what to do, and a link to the dashboard when the dashboard has a public address. An email has the same in its body, and a subject such as `[Geass] Deploy failed (2 alerts)`.

The messages say what is wrong and where, and the dashboard says why: the reason a build failed is in its log, and the reason a deploy failed is on the service. They do not carry text from a service's own output.

Text that came from a person, such as a service's name, is made safe first: a service named `<!channel>` does not notify the whole Slack workspace.

## See what is happening

The page shows:

- Whether **Alertmanager** can be reached. If it cannot, nothing can be sent, and the page says so.
- **Firing now**: what Alertmanager holds and has not been told is over, with the project, how long, and what it is.
- A warning when Alertmanager has failed to send requests to Slack or the mail server since it started.

**Send test** puts a test alert into Alertmanager, the way a real one goes, so it proves the whole path and not only that the address is right. It can take up to a minute: Alertmanager has to have loaded the channel, which takes a moment after you add or change one, and then it says whether it sent the message. Each test is its own alert, so you can test the same channel again at once. A test never reaches any channel but the one you chose, even if it is paused. A failure is read from Alertmanager's log, which names the channel it failed for; where the dashboard cannot read the log it goes by Alertmanager's counts, which are shared by every channel that sends through Slack or the mail server, so another channel's trouble can show as this one's.

To silence an alert for a while, use Alertmanager itself. It is not exposed outside the cluster:

```bash
kubectl -n monitoring port-forward svc/geass-kube-prometheus-stac-alertmanager 9093
```

Then open `http://localhost:9093`. Prometheus shows the alerts as they are made, and the metric behind most of them, `geass_problem`, with the project, environment and resource.

## Set it up as code

Everything on the page is in `spec.alerting` of the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/). The Slack URLs and the mail password stay in Secrets.

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

Do not apply it as a whole over a platform configuration that has other settings: `kubectl apply` of a partial manifest can drop fields. Use `kubectl patch` or edit the object. The field reference is in [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/#spec-fields).

## How it works

Three things in the cluster, in order:

1. **The operator says what is wrong.** The operator that holds the lease reads the status its controllers already write, every 30 seconds, and exports one series of the metric `geass_problem` for each problem: a build that failed, a resource whose `Ready` condition says Geass gave up, a workload with nothing available, a database whose backups are at risk, a node that is not ready, a cluster condition. It also exports which project owns each namespace, so that a crashing pod can be said to be in a project. Prometheus scrapes it with the operator's other metrics. An operator that has not looked for five minutes says nothing, so its last problems do not live on.
2. **Prometheus decides when it counts.** The `PrometheusRule` `geass-alerts` in `monitoring` turns those series, and Kubernetes's own metrics for crashing pods and filling volumes and disks, into alerts, each waiting as long as the table above says. Every alert carries the labels the channels' filters match on: `geass_kind`, `project`, `environment`, `severity`.
3. **Alertmanager sends it.** Geass keeps one `AlertmanagerConfig`, `geass-alerts` in `geass-system`, with a receiver and a route for each channel, made from the channels you set up. The dashboard writes it as soon as you save, and the operator puts it right if it is edited or deleted, and when you change the settings with `kubectl`. With no channel that can send, there is none. The Prometheus operator reads it, and reads the Secrets it refers to, and gives Alertmanager its configuration.

A channel that is missing what it needs to send, a Slack channel without its URL or an email channel before the mail server is set up, is left out of the configuration. That is on purpose: the Prometheus operator throws away a whole `AlertmanagerConfig` that refers to a Secret key it cannot find, and one channel without its secret must not silence the others.

The monitoring chart's own rules that make a warning give way to a critical one of the same name do not apply to Geass's alerts. Geass's alerts have no namespace, which those rules compare, so a critical failure in production would otherwise have silenced the warning about the same failure in staging.

If you run your own Alertmanager, point the dashboard's view at it with `GEASS_ALERTMANAGER_URL` on the operator. The alerts carry their labels, so you can route them there yourself, by `geass_kind` and `project`.

## Limits and safety

- Up to 20 channels, up to 20 addresses on an email channel, and up to 100 projects or environments in a channel's filters.
- Only a signed-in admin can see or change alert settings. An API token cannot, not even an admin token: a token that could change where alerts go could also silence them.
- Alertmanager, not Geass, connects to Slack and to the mail server, so the guard that [outbound webhooks](/docs/guides/github-and-builds/#outgoing-webhooks) have at the moment they connect does not apply. Geass checks the host when you save: one that is or resolves to a loopback, link-local or, unless the operator runs with `GEASS_WEBHOOKS_ALLOW_PRIVATE_NETWORKS=true`, private address is refused. It cannot see a name that resolves differently later.
- A Slack address must be `https`.
- Text a person typed, such as a channel's name or an address, is never written into a message template: Alertmanager runs them as templates. An address with braces in it is refused for the same reason.

## Troubleshooting

| What you see | What it means |
| --- | --- |
| `Alertmanager could not be reached ... It is part of the monitoring add-on` | The monitoring add-on is off, or its Alertmanager is not running. Turn the add-on on (`spec.addons.monitoring.enabled`) and look at `kubectl -n monitoring get pods` |
| `Saved. Nothing can be sent until the monitoring add-on is installed` | The cluster has no `AlertmanagerConfig` kind yet, which the add-on brings. What you saved is kept, and works once it is there. It is shown as a notice, not as a failure |
| `Alertmanager has not loaded this channel yet` | A test was sent before Alertmanager had the new channel. It takes up to a minute: try again |
| `Alertmanager could not post to Slack` | The webhook URL was removed or is wrong, or the `monitoring` namespace cannot reach Slack. Alertmanager's log has Slack's answer: `kubectl -n monitoring logs alertmanager-geass-kube-prometheus-stac-alertmanager-0 -c alertmanager` |
| `Alertmanager could not send the email` | The mail server's login is wrong, it refused the sender (the From address or its domain is not verified with your provider), or the `monitoring` namespace cannot reach it. Alertmanager's log has the server's answer |
| `Alertmanager has not reported sending it yet` | The test is still on its way, or stuck. Look in the channel in a minute |
| `No Slack webhook URL is saved for this channel.` | The channel exists but its secret does not. Edit it and paste the URL again |
| `Set up the mail server first.` | An email channel needs the mail server under **Mail server** |
| `port 465 is encrypted from the first byte` / `TLS from the start is only used on port 465` | Alertmanager connects encrypted from the first byte only on port 465. Use STARTTLS on 587, or TLS on 465 |
| `a password cannot be sent over a connection that is not encrypted` | Alertmanager will not send a password in the clear. Choose STARTTLS or TLS, or remove the username |
| `... is a private network address, which cannot be used` | The host is on a private network. See the limits above |
| `could not connect to host:port: connect: connection refused` (or `i/o timeout`) | **Test connection**, from the operator, could not reach the mail server: the host or port is wrong, or a firewall blocks outbound mail |
| `does not offer STARTTLS` | The port does not do STARTTLS. Try TLS from the start with port 465, or the provider's documented port |
| `could not set up TLS ... certificate ...` | The mail server's certificate is not trusted, or is for another name than the host you entered |
| `the mail server refused the username and password` | The SMTP credentials are wrong. Many providers use credentials for SMTP that differ from the account's password |
| Nothing arrives and **Firing now** is empty | Nothing is wrong, or Prometheus is not loading the rules: `kubectl -n monitoring get prometheusrule geass-alerts`, and the **Rules** page of Prometheus |
| Something is firing and nothing arrives | The channel's filters leave it out, Alertmanager has failed to send (the warning on the page), or the alert is silenced in Alertmanager |

## Related

- [Logs, metrics and terminal](/docs/guides/observability/#metrics-and-alerts-for-the-operator): the monitoring add-on, and the alerts on the operator's own health, which are cluster problems to these channels.
- [GitHub and builds](/docs/guides/github-and-builds/): what Geass reports to GitHub, and outgoing webhooks for deployments.
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
