---
title: "Mail relay"
description: "Let organizations on a shared cluster send mail through a relay that Geass runs, each with its own login and limits, to a mail service that you select."
---

On a cluster that several [organizations](/docs/guides/organizations-and-workspaces/) share, [outbound limits](/docs/guides/outbound-limits/) stop their pods from reaching mail servers outside the cluster. The **mail relay** gives the organizations a way to send mail that you control. The relay is an SMTP submission server that Geass runs in `geass-system`. Each organization signs in to the relay with its own login, and the relay holds each organization to its own limits. The relay gives every message to one mail service that you select, the **upstream**. The upstream can be Amazon SES, Postmark, Mailgun, or your company's own relay.

The relay is off until you set it. Without the relay, nothing in the cluster changes.

## How it works

1. An organization's service connects to `geass-mail-relay.geass-system.svc` on port 587. The service starts TLS (STARTTLS), checks the relay's certificate, and signs in with its organization's login. All of these values come from the Secret `geass-mail` in the service's namespace.
2. The relay checks the message. These conditions must be true:
   - The organization is within its limits.
   - The sender is at a domain that the organization can send from.
   - No recipient is at a domain that the relay does not send to.
3. While the client waits, the relay opens a connection to the upstream and sends the message. The relay answers the client with the upstream's answer. `250` means that the upstream took the message.

The relay answers only after the upstream answers, so the relay keeps no queue. If the upstream did not take a message, the relay refuses the message to the client. The client still has the message and can try again. The relay is the operator's own program (`manager mailrelay`, from the operator's image). The relay runs as the Deployment `geass-mail-relay`, with one replica. See [Where the counts are kept](#where-the-counts-are-kept).

## Before you start

- Organizations are set up, and projects are given to them.
- **An account at the upstream**, with SMTP credentials. Every domain that the organizations send from must be verified at the upstream. Most services send only from verified domains or addresses, for example SES and Postmark.
- **Outbound limits**, so that organizations cannot go around the relay. Set `spec.tenancy.egress` with mail `Blocked`, which is the default. Without outbound limits, a pod can still reach any mail server directly.
- **The relay must reach the upstream's port** from the cluster. The relay runs in `geass-system`, so outbound limits never apply to it. Many clouds close port 25 to the internet, and some clouds also close port 587. If your ports are closed, use the upstream's other ports. SES also listens on port 2587, and on ports 465 and 2465 with TLS from the first byte. Postmark also listens on port 2525.
- **The relay checks the upstream's certificate** against the public certificate authorities in the operator's image. The released image (`ghcr.io/degoke/geass`, built on distroless) has these authorities. An image built from `Dockerfile.prebuilt` (`FROM scratch`) has none. With that image, every TLS connection to the upstream fails.

## Set it up

Put the upstream's SMTP login in a Secret in `geass-system`, with the keys `username` and `password`:

```bash
kubectl -n geass-system create secret generic mail-upstream \
  --from-literal=username='AKIA...' --from-literal=password='...'
```

Then turn the relay on in the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/):

```yaml title="platform-mail.yaml (spec.tenancy.mail)"
spec:
  tenancy:
    mail:
      relay:
        enabled: true
        upstream:
          host: email-smtp.eu-west-1.amazonaws.com
          port: 587                 # the default; 465 with tls: TLS
          tls: StartTLS             # the default: the upstream must offer STARTTLS, or nothing is sent
          credentialRef:
            name: mail-upstream
        limits:                     # for every organization, unless its own spec.mail.limits says otherwise
          messagesPerMinute: 60
          messagesPerHour: 600
          messagesPerDay: 2000
          recipientsPerMessage: 50
          maxMessageSize: 10Mi
          connections: 5
        requireSenderDomain: true   # an organization with no sender domain sends nothing
        deniedRecipientDomains:
          - example.invalid
```

Merge these settings in with a patch. Do not apply the fragment by itself:

```bash
kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"tenancy":{"mail":{"relay":{
  "enabled":true,
  "upstream":{"host":"email-smtp.eu-west-1.amazonaws.com","credentialRef":{"name":"mail-upstream"}},
  "requireSenderDomain":true}}}}}'
```

Give each organization the domains that it can send from. If an organization needs limits that are different from the platform's limits, give it its own limits:

```bash
kubectl -n geass-system patch geassorganization org-1a2b3c4d --type merge -p '{"spec":{"mail":{
  "allowedSenderDomains":["acme.example"],
  "limits":{"messagesPerDay":10000}}}}'
```

Within a few seconds, Geass creates these items:

- The relay.
- The relay's certificate.
- A login for each organization.
- The Secret `geass-mail` in each environment of each organization's project.

Then check the condition:

```bash
kubectl -n geass-system get geassplatformconfig platform -o jsonpath='{.status.conditions[?(@.type=="MailRelayReady")]}{"\n"}'
```

To turn the relay off, run `kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"tenancy":{"mail":null}}}'`, or set `enabled: false`. Geass removes the relay, its certificate, every login that it made, and every `geass-mail` Secret. Geass removes nothing else. The upstream's Secret is yours, so Geass leaves it.

| Field | Meaning |
| --- | --- |
| `enabled` | Runs the relay. |
| `upstream.host`, `.port` | The upstream. The port defaults to 587, or to 465 with `tls: TLS`. |
| `upstream.tls` | `StartTLS` (the default): the relay connects in plain text and requires STARTTLS. If the upstream does not offer STARTTLS, the relay refuses to send. `TLS`: TLS from the first byte. `None`: plain text, for an upstream inside the cluster or on a private network. You cannot use `None` with `credentialRef`, because the relay never sends the upstream's password in plain text. The upstream's certificate must be valid for `host`. |
| `upstream.credentialRef.name` | A Secret in `geass-system` with `username` and `password`. The relay signs in with AUTH PLAIN, over TLS only. The relay reads the Secret from a volume, so the relay uses a new password within a minute or two, without a restart. If this field is not set, the relay does not sign in. |
| `limits` | What each organization can send. See [Limits](#limits). |
| `allowedSenderDomains` | The domains that organizations can send from. An organization's own `spec.mail.allowedSenderDomains` replaces this list. |
| `requireSenderDomain` | Refuses all mail from an organization that has no allowed sender domain, neither its own nor the platform's. Without this field, such an organization can send from any address. |
| `deniedRecipientDomains` | The relay never sends to these domains, for any organization. A domain also covers the domains below it. |

## Choose an upstream

Any service that accepts mail by SMTP submission with a login works. Check the documentation of the service for the current host names and ports.

| Service | Host | Notes |
| --- | --- | --- |
| Amazon SES | `email-smtp.<region>.amazonaws.com` | Use the SMTP credentials of SES, not an access key. Verify the domain of each organization, and set up its DKIM in SES. A new account is in the SES sandbox, and it can send only to verified addresses. |
| Postmark | `smtp.postmarkapp.com` | The username and the password are both the API token of the server. Ports 587 and 2525. |
| Mailgun | `smtp.mailgun.org` (or `smtp.eu.mailgun.org`) | Use an SMTP login of the sending domain. |
| Your own relay | Its address | Inside the cluster or on a private network, you can use `tls: None` without a login. The relay's network policy lets it reach the upstream's port on any address except the cloud's metadata. |

The relay sends the mail of every organization through the one upstream account, so the upstream sees one sender. An organization that sends spam within its limits still harms the reputation of that account. The limits set a maximum on the harm that an organization can do. The limits cannot prevent the harm.

## Limits

Each field of an organization's `spec.mail.limits` replaces the platform's `limits` for that field. If neither sets a field, the field has the relay's default.

| Field | Default | At the limit |
| --- | --- | --- |
| `messagesPerMinute` | 60 | `450 4.7.1` at `MAIL FROM`. This is a temporary failure, so the sender tries again later |
| `messagesPerHour` | 600 | The same |
| `messagesPerDay` | 2000 | The same |
| `recipientsPerMessage` (1 to 1000) | 50 | `452 4.5.3` for the recipient after the limit. This code tells the sender to send to the other recipients in another message. The relay sends to the recipients that it already took. |
| `maxMessageSize` (1Ki to 50Mi) | `10Mi` | `552 5.3.4`, when the message announces its size (`SIZE=`), or immediately when the message grows past the limit. The upstream drops a message that is cut off this way, because the relay closes that connection before the end of the data. |
| `connections` (1 to 1000) | 5 | `454 4.7.0` to the sign-in of one connection too many |

- **What counts.** Rates count messages, not recipients. A message to 50 people counts one time. For this reason, the relay also has a limit on recipients per message.
- **When it counts.** A message counts from the moment that the relay takes its `MAIL` command. This prevents many connections that send at the same time from all taking the last free place. If the relay then does not send a message, the relay gives the place back. This includes a message that the relay refused, a message that grew too large, and a message that the upstream did not take.
- **The windows** are sliding: any 60 seconds, any 60 minutes, and any 24 hours. The relay keeps the windows in small buckets: one second for the minute, and one minute for the hour and the day. A message counts for at least the whole window, and for at most one more bucket. So no window ever holds more than the limit, and the relay refuses exactly the message after the limit.
- **Zero.** A rate of `0` sends nothing. If you set `messagesPerMinute: 0` on one organization, that organization's mail stops immediately, and no other organization is affected.

**Sender domains.** The relay checks the envelope sender (`MAIL FROM`) and every address in the `From` and `Sender` headers. All of these addresses must be at an allowed domain or below one. For example, `acme.example` also covers `mail.acme.example`. When the relay checks senders, it refuses a message without a `From` header, and it refuses the empty sender (`MAIL FROM:<>`). The refusal is `550 5.7.1`. The relay only compares domains. The upstream proves that a domain belongs to the organization, through its domain verification and DKIM.

**Recipient domains.** The relay refuses a recipient at a denied domain, or below one, with `550 5.7.1`. The relay still takes the other recipients of the same message.

The relay gives these answers:

| Answer | When |
| --- | --- |
| `523 5.7.10` | `AUTH` before STARTTLS. The relay does not offer AUTH until TLS is on. |
| `530 5.7.0` | `MAIL` without a sign-in. |
| `535 5.7.8` | A wrong username or password. After three on one connection, the next attempt closes the connection. |
| `454 4.7.0` | Too many connections of the organization at the same time. |
| `421 4.7.0` | Too many connections in total: 256, over every organization and every connection that has not signed in yet. |
| `450 4.7.1` | A rate limit. |
| `452 4.5.3` | Too many recipients. |
| `552 5.3.4` | Too large. |
| `554 5.6.0` | The relay could not read the message: a line over 2000 characters, or a header over 256 KiB. The message does not count. |
| `550 5.7.1` | A sender that the organization cannot use, a recipient at a denied domain, or a login whose organization was removed. |
| `451 4.4.1` | The relay could not reach the upstream, or the upstream did not offer STARTTLS, or the upstream presented a certificate that is not valid for its name. |
| `451 4.7.0` | The upstream refused the relay's own login. The platform must fix this problem, so the relay tells the client to try again. |
| `451 4.x.x` | The upstream deferred the message. |
| The upstream's `5xx` code | The upstream refused the message permanently, for example for an unverified sender. The relay passes on the code, but not the upstream's own words, because those words can name the platform's account. |
| `250` | The upstream took the message. |

## Where the counts are kept

**The counts are only in the memory of the one replica of the relay.** This has these results:

- **A restart starts every count from zero.** The relay restarts when the operator is upgraded, when its node is drained, or when it runs out of memory. After a restart, an organization that sent its whole limit for the day can send that much again. If you need a firm daily limit, also set one at the upstream. For example, SES has a sending quota for the whole account.
- **There is one replica, and there must be only one.** Two replicas would each count separately. Each replica would let an organization send its full limit. Geass keeps the Deployment at one replica, and Geass sets it back if someone scales it. A new version replaces the old pod by stopping the old pod first. So during an upgrade, or while the node is replaced, nothing answers on port 587 for a few seconds to a minute. Clients see a refused connection, and a mail library usually reports that as a failure. So send from a job that tries again.
- **A change of settings does not restart the relay.** Every few seconds, the relay reads its configuration and certificate again. It reads them from the Secrets that the kubelet keeps up to date. The relay keeps what it has counted. A change reaches the relay within a minute or two. This includes a new organization, new limits, a rotated login, and a renewed certificate.

`GET /api/v1/mail/usage` tells you when the current counts started (`countedSince`).

## How organizations use it

Each environment of each organization's project has a Secret `geass-mail`. This includes pull request environments, after their namespace exists. A project that is not in an organization gets no `geass-mail` Secret, and builds cannot reach the relay at all. Geass does not put the Secret into any pod automatically. A service that sends mail must reference the Secret.

| Key | Value |
| --- | --- |
| `SMTP_HOST` | `geass-mail-relay.geass-system.svc` |
| `SMTP_PORT` | `587` |
| `SMTP_SECURITY` | `starttls` |
| `SMTP_USERNAME` | The organization's name, for example `org-1a2b3c4d` |
| `SMTP_PASSWORD` | The organization's password: 32 random bytes, base64url |
| `SMTP_CA_CERT` | The certificate of the authority that signed the relay's certificate (PEM) |

In the service's manifest, `envFrom` changes every key into an environment variable. `secretRefs` also mounts the keys as files under `/secrets/refs/geass-mail/`:

```yaml
spec:
  envFrom:
    - secretRef:
        name: geass-mail
  secretRefs:
    - name: geass-mail
```

**How a client checks the relay.** An authority that Geass keeps signs the relay's certificate. This authority is not one of the public authorities. To check the relay, do these steps in the client:

- Trust `SMTP_CA_CERT` as the certificate authority of the client.
- Connect with the name `SMTP_HOST`.
- Require STARTTLS.

Do not turn certificate checks off. Without these checks, anything that can take the relay's address can read the organization's password. For example, with Nodemailer:

```js
const transport = nodemailer.createTransport({
  host: process.env.SMTP_HOST, port: Number(process.env.SMTP_PORT), secure: false, requireTLS: true,
  auth: { user: process.env.SMTP_USERNAME, pass: process.env.SMTP_PASSWORD },
  tls: { ca: process.env.SMTP_CA_CERT, servername: process.env.SMTP_HOST },
});
```

and with Python's `smtplib`:

```python
context = ssl.create_default_context(cadata=os.environ["SMTP_CA_CERT"])
with smtplib.SMTP(os.environ["SMTP_HOST"], int(os.environ["SMTP_PORT"])) as smtp:
    smtp.starttls(context=context)
    smtp.login(os.environ["SMTP_USERNAME"], os.environ["SMTP_PASSWORD"])
    smtp.send_message(message)
```

The relay accepts AUTH `PLAIN` and `LOGIN`. A line can have at most 2000 characters, as RFC 5321 expects. The relay refuses a message with a longer line with `554 5.6.0`, and a longer command closes the connection.

## Networking

**Organizations reach the relay, whatever their outbound limits say.** The outbound limits close ports to addresses *outside* the cluster. The relay is a pod of the cluster, and the pods of an organization reach the pods of the cluster on every port. See [What Geass writes](/docs/guides/outbound-limits/#what-geass-writes). This is also true with `allowedPorts`: `allowedPorts` does not need to list 587, and the relay needs no `allow` entry. In the same way, a project with project isolation still reaches the relay. Isolation limits who reaches a project, not where the pods of a project go. Builds reach nothing in the cluster except its DNS, so builds cannot reach the relay.

**The relay's own policy** is `geass-mail-relay` in `geass-system`:

| Direction | Allowed |
| --- | --- |
| In, port 1587 (the Service's 587) | Every pod of the cluster. Each connection must still sign in. |
| In, port 9187 (health, metrics, usage) | `geass-system` (the dashboard) and `monitoring` (Prometheus). Metrics and usage also need the admin token. |
| Out | The cluster's DNS (`kube-system`, port 53), and the upstream's port on any address or pod, except the cloud's metadata range. Nothing else. This includes the cluster's API: the relay never uses it, because the relay's pod has no service account token. |

Policies work only on a network plugin that enforces them. Without such a plugin, the logins still protect the submission port, and the token still protects the admin port. A dashboard that runs [outside the cluster](/docs/operations/dashboard-only/) cannot reach the admin port. To read mail usage from that dashboard, make the port reachable to it yourself, and set `GEASS_MAIL_RELAY_URL`.

## What it logs

The relay writes one JSON line for each event. To read the lines, run `kubectl -n geass-system logs deploy/geass-mail-relay`:

| Message | Fields |
| --- | --- |
| `signed in` | `org`, `remote` (the client's address) |
| `sign-in refused` | `org` (only when the username is an organization's name, otherwise `unknown`), `remote`, `failures` |
| `message relayed` | `org`, `fromDomain`, `recipients` (how many), `bytes`, `durationMs` |
| A refusal (`a message over a rate limit was refused`, and others) | `org`, `reason`, `remote`, and the limit or the domain that applies |
| `the upstream did not take a message` | `org`, `kind` (`unreachable`, `deferred`, `refused`, `auth`), `upstreamCode`, `recipients`, `durationMs` |
| `configuration loaded`, `certificate loaded`, `upstream credentials loaded`, `could not read the relay's ...` | What the relay read, and why the relay could not read it |

The relay never logs these items:

- A password.
- What a client sent to sign in.
- The upstream's login.
- The admin token.
- An address. The relay logs only the domain of the sender.
- A subject or a body.
- The upstream's reply text.

A test does these steps, and then searches the whole log for each secret:

- It signs in correctly.
- It signs in incorrectly, with a password typed where the username goes.
- It sends through an upstream that requires a login.

## Metrics and usage

With the monitoring add-on, Geass makes Prometheus scrape the relay. Geass uses a ServiceMonitor `geass-mail-relay` in `monitoring`, with a copy of the admin token in the Secret `geass-mail-relay-metrics` next to it. The `org` label only holds the name of an organization that the relay knows, or `unknown`.

| Metric | Labels | Meaning |
| --- | --- | --- |
| `geass_mail_relay_messages_accepted_total` | `org` | Messages that the upstream took |
| `geass_mail_relay_recipients_accepted_total`, `geass_mail_relay_bytes_accepted_total` | `org` | The recipients and bytes of those messages |
| `geass_mail_relay_rejections_total` | `org`, `reason` (`rate_minute`, `rate_hour`, `rate_day`, `recipients`, `size`, `sender_domain`, `recipient_domain`, `connections`, `organization_removed`) | What the relay refused |
| `geass_mail_relay_upstream_failures_total` | `org`, `kind` (`unreachable`, `deferred`, `refused`, `auth`) | Messages that the upstream did not take |
| `geass_mail_relay_auth_failures_total` | `org` | Failed sign-ins |
| `geass_mail_relay_connections` | `org` | Connections that are signed in now |
| `geass_mail_relay_upstream_duration_seconds` | `result` | The time to give one message to the upstream |
| `geass_mail_relay_config_reloads_total` | `result` (`ok`, `failed`) | Times that the relay read the configuration, the certificate, or the upstream login again |

`GET /api/v1/mail/usage` answers with the counts of each organization. You can add `?org=` to the request. Only the platform's administrators can call this endpoint. See the [REST API](/docs/reference/rest-api/#mail-relay-usage).

## Rotate credentials

| What | How | Effect |
| --- | --- | --- |
| An organization's login | `kubectl -n geass-system annotate secret geass-mail-credentials-org-1a2b3c4d geass.dev/rotate-mail-credentials=now` | A new password goes into every `geass-mail` of the organization. The old password continues to work for 24 hours. Services read the Secret when they start, so restart the services that send mail within that day. The organization gets the event `MailCredentialsRotated`. |
| Revoke a login immediately | `kubectl -n geass-system delete secret geass-mail-credentials-org-1a2b3c4d` | Geass makes a new password. The old password stops working when the relay reads its configuration again, after a minute or two. Services fail to sign in until they restart. |
| Stop an organization's mail | `messagesPerMinute: 0` in its `spec.mail.limits` | The relay refuses every message with `450 4.7.1`, and the organization keeps its login. |
| The upstream's password | Change the Secret that `credentialRef` names | The relay uses the new password within a minute or two, without a restart. |
| The relay's certificate | No action is necessary. Geass renews the certificate 30 days before its 90 days end. To force a new certificate, delete `geass-mail-relay-tls`. | The authority stays the same, so clients need nothing new. |
| The authority | Delete `geass-mail-relay-ca` | Geass makes a new authority. The authority lasts ten years, and Geass replaces it one year before it ends. Every `geass-mail` gets the new `SMTP_CA_CERT`. The services that loaded the old `SMTP_CA_CERT` must restart. |
| The admin token | Delete the Secret `geass-mail-relay` | Geass makes the Secret again with a new token. The dashboard and Prometheus read the new token from the Secret. |

## The condition

While the relay is set, the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/#mailrelayready) has the condition `MailRelayReady`:

| Status | Reason | Meaning |
| --- | --- | --- |
| `True` | `Ready` | The relay's pod is ready. The message names the relay's address and the upstream. |
| `False` | `Starting` | The pod is not ready yet. |
| `False` | `UpstreamCredentialsMissing` | The Secret that `credentialRef` names is missing, or it does not have `username` or `password`. The pod waits for the Secret. |
| `False` | `ImageUnknown` | The operator could not read its own Deployment to find its image. |
| `False` | `InvalidSettings` | Geass cannot apply the settings. For example, a `maxMessageSize` over 50Mi, or a login over plain text that the CRD did not catch. The relay keeps the configuration that it had, and Geass records the `Warning` event `MailRelaySettingsInvalid` one time. |

## What it does not do

- **It does not scan content.** There is no spam, malware, or phishing filter. The relay sends a message that is within the limits.
- **It does not manage DKIM, SPF, or DMARC.** Set up signing at the upstream, for example SES Easy DKIM or Postmark's DKIM. Publish the DNS records of each organization's domain yourself.
- **It does not handle bounces or complaints.** Bounces and complaints go from the upstream to the envelope sender, and the relay never sees them. The upstream keeps the suppression lists and the complaint rates.
- **It has no queue and does not retry.** If the upstream did not take a message, the relay refuses the message to the client. The client must retry.
- **It keeps no reputation for each recipient or each destination**, and it does not warm up new senders.
- **Its counts do not stay after a restart, and it cannot run more than one replica.** See [Where the counts are kept](#where-the-counts-are-kept).
- **It has one upstream for every organization**, so the organizations share the limits and the reputation of that account.
- **It is not reachable from outside the cluster**, and it does not offer TLS from the first byte (port 465). It offers only STARTTLS on port 587.

## What has been tested

The relay is tested in-process. The go-smtp client talks to the relay, the same as an organization's service. Behind the relay is a fake upstream, which is another go-smtp server in the test. The tests check these items:

- TLS before the sign-in, and a check of the certificate by name and authority.
- Wrong, missing, and foreign logins are refused, and a connection closes after three failures.
- A rotated password is accepted until its time ends.
- Each rate is refused exactly at its limit, and accepted again after its window.
- Recipients, size (announced and actual), and connections are refused exactly at their limits.
- Organizations are kept apart: one organization at its limit does not slow another.
- Sender domains are checked in the envelope and the headers, and denied recipient domains are refused.
- The upstream is down, defers, refuses a recipient or the message, or refuses the relay's login.
- STARTTLS and TLS to the upstream, with a check of its certificate.
- The relay answers nothing before the upstream takes the message.
- Exactly the limit of each organization is sent when many connections send at the same time, under the race detector.
- The log is searched for every secret.

The controller is tested against fake clients and a real API server (envtest). The tests cover the path from the platform's settings to the resources that Geass writes. These resources are the relay's Deployment, Service, policy, certificate, and configuration, and the login and `geass-mail` of every organization. The tests check these items:

- Changes, rotation, and renewal.
- Projects that move between organizations.
- Edits made by hand, which Geass puts back.
- Settings that Geass cannot apply.
- Nothing is written when the relay is not set, and everything is removed when the relay is turned off.
- A second pass writes nothing.

The network policies are checked port by port, with the same independent reading of NetworkPolicy rules that tests the outbound limits.

**On a real K3s (v1.36.4+k3s1)**, the relay ran from the operator's own image as the Deployment `geass-mail-relay`. Its upstream was a Python `aiosmtpd` server in the cluster (plain, `tls: None`, no login). An organization's service used Python's `smtplib` in the same way as the example above. Five seconds after the setting was patched, the relay was enabled, and `MailRelayReady` said `Ready`. These were the results:

- **TLS and logins.**
  - `AUTH` before `STARTTLS` got `523 5.7.10`, and the first `EHLO` listed `STARTTLS` and no `AUTH`.
  - `MAIL` before a sign-in got `530 5.7.0`.
  - The system's authorities refused the relay's certificate. The Service's cluster IP, which is not a name in the certificate, failed verification.
  - Each of these got `535 5.7.8`: a wrong password, an unknown username, a password typed as the username, and another organization's name with the first organization's password. The relay closed the connection after three failed `AUTH` attempts on it.
- **Limits and rules.** The platform's limits were 5 messages a minute, 3 recipients, 100Ki, and 3 connections. One organization had its own limit of 2 messages a minute. The results were:
  - `450 4.7.1` at `MAIL` after the limit. The count of the other organization did not change, and the relay accepted a message after the window.
  - `452 4.5.3` for the fourth recipient.
  - `552 5.3.4` for a message over the limit, with or without a `SIZE=` announcement.
  - `454 4.7.0` for the fourth connection.
  - `550 5.7.1` for each of these:
    - A sender at another domain, in the envelope or in `From`.
    - No `From`.
    - The null sender.
    - One organization's login that sent as the domain of the other organization.
    - A recipient at a denied domain. A recipient below the denied domain was also refused, and the relay took the other recipient of the message.
  - The upstream refused a recipient with a `550 5.1.1` whose text contained an account name. The client got `550 5.1.1`, but not the text.
- **The upstream down.** Six tries within one minute, with a limit of five, got `451 4.4.1`. So none of the tries counted. The relay accepted the next message immediately when the upstream was back.
- **A restart** of the relay's pod gave an organization that had reached its limit its allowance back in the same minute. `countedSince` in `GET /api/v1/mail/usage` changed. The relay did not answer for about six seconds.
- **The admin port.** `/metrics` without the token was refused (`401`), and `/metrics` with the token answered. A pod of an organization could not reach port 9187. The metrics for each organization, `GET /api/v1/mail/usage`, and the structured log lines were as described. The log contained none of the passwords that the test saw: the current, rotated, and new passwords of the organization, and the admin token.
- **Rotation and revocation.**
  - After the annotation, the new password was in `geass-mail` within one second. The relay accepted the new password 36 to 83 seconds later, and the old password could still sign in.
  - After the login Secret was deleted, Geass made a new password within one second. The relay refused the old password and the rotated-out password about 79 seconds later. At 23 seconds, the old password could still sign in.
- **Settings.**
  - The CRD refused `tls: None` with a `credentialRef`, `recipientsPerMessage: 5000`, and `enabled` without an `upstream`.
  - A `maxMessageSize` of 80Mi set `MailRelayReady` to `False` `InvalidSettings`, and made one `MailRelaySettingsInvalid` event. The relay continued to enforce its old 100Ki.
  - A `credentialRef` to a Secret that does not exist gave `UpstreamCredentialsMissing`.
- **Off.** `{"spec":{"tenancy":{"mail":null}}}` removed these items: the Deployment, Service, policy, certificate, authority, admin Secret, every login, every `geass-mail`, the ServiceMonitor, the token copy, and the condition. It did not change the platform's own upstream.
- **Under gVisor,** the relay accepted a message from an organization's service in a sandboxed pod.
- **Direct mail** with the outbound limits on, over IPv4:
  - From a pod of an organization, `smtp.gmail.com` ports 587 and 465 were refused. `imap.gmail.com` port 993, which is not a mail port, was reached.
  - From a pod that is not in an organization, all three ports were reached.
  - After `spec.egress.mail: Open` was set on the organization, ports 587 and 465 were reached from its pod.
  - Port 25 was not tried, because AWS throttles port 25 for every instance.

**Not tested:**

- The relay against a real mail service (SES, Postmark, or any other). This means TLS or STARTTLS to the upstream with a check of its certificate, and a login to the upstream.
- A mail library other than Python's `smtplib`. The Nodemailer example is not tried.
- The reason `ImageUnknown`.
- Prometheus scraping the ServiceMonitor.
- A dashboard outside the cluster that reads the usage.

## Related

- [Outbound limits](/docs/guides/outbound-limits/)
- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/)
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/) and [GeassOrganization](/docs/reference/resources/geass-organization/)
- [Environment variables](/docs/guides/environment-variables/)
- [Security](/docs/operations/security/)
