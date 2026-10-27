---
title: "Mail relay"
description: "Let organizations that share a cluster send mail through a relay Geass runs, each with its own login and limits, to a mail service you choose."
---

On a cluster that several [organizations](/docs/guides/organizations-and-workspaces/) share, [outbound limits](/docs/guides/outbound-limits/) stop their pods from reaching mail servers outside the cluster. The **mail relay** gives them a way to send mail that you control. It is an SMTP submission server that Geass runs in `geass-system`. Each organization signs in to it with its own login and is held to its own limits. The relay hands every message to one mail service that you choose, the **upstream**: Amazon SES, Postmark, Mailgun, or your company's own relay.

It is off until you set it. Without it, nothing in the cluster changes.

## How it works

1. An organization's service connects to `geass-mail-relay.geass-system.svc` on port 587. It starts TLS (STARTTLS), checks the relay's certificate, and signs in with its organization's login. All of these come from the Secret `geass-mail` in its namespace.
2. The relay checks the message. The organization must be within its limits, the sender must be at a domain it may send from, and no recipient may be at a domain the relay does not send to.
3. While the client waits, the relay opens a connection to the upstream and sends the message. It answers the client with the upstream's answer. `250` means the upstream has taken the message.

Because the relay answers only after the upstream has answered, it keeps no queue. A message the upstream did not take is refused to the client, which still has it and can try again. The relay is the operator's own program (`manager mailrelay`, from the operator's image). It runs as the Deployment `geass-mail-relay`, with one replica (see [Where the counts are kept](#where-the-counts-are-kept)).

## Before you start

- Organizations are set up, and projects are given to them.
- **An account at the upstream**, with SMTP credentials, and every domain the organizations will send from verified there. Most services send only from verified domains or addresses: SES does, and so does Postmark.
- **Outbound limits**, so that organizations cannot go around the relay: `spec.tenancy.egress` with mail `Blocked`, which is the default. Without them, a pod can still reach any mail server directly.
- **The relay must reach the upstream's port** from the cluster. It runs in `geass-system`, so outbound limits never apply to it. Many clouds close port 25 to the internet, and some close 587 too. Use the upstream's other ports when yours are closed: SES also listens on 2587 and, with TLS from the first byte, on 465 and 2465. Postmark also listens on 2525.
- **The relay checks the upstream's certificate** against the public certificate authorities in the operator's image. The released image (`ghcr.io/degoke/geass`, built on distroless) has them. An image built from `Dockerfile.prebuilt` (`FROM scratch`) has none, so every TLS connection to the upstream fails with it.

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

Merge it in with a patch rather than applying the fragment on its own:

```bash
kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"tenancy":{"mail":{"relay":{
  "enabled":true,
  "upstream":{"host":"email-smtp.eu-west-1.amazonaws.com","credentialRef":{"name":"mail-upstream"}},
  "requireSenderDomain":true}}}}}'
```

Give each organization the domains it may send from, and its own limits if they should differ from the platform's:

```bash
kubectl -n geass-system patch geassorganization org-1a2b3c4d --type merge -p '{"spec":{"mail":{
  "allowedSenderDomains":["acme.example"],
  "limits":{"messagesPerDay":10000}}}}'
```

Within a few seconds Geass creates the relay, its certificate, a login for each organization, and the Secret `geass-mail` in each environment of each organization's project. Then check the condition:

```bash
kubectl -n geass-system get geassplatformconfig platform -o jsonpath='{.status.conditions[?(@.type=="MailRelayReady")]}{"\n"}'
```

To turn it off, run `kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"tenancy":{"mail":null}}}'`, or set `enabled: false`. Geass removes the relay, its certificate, every login it made and every `geass-mail` Secret. It removes nothing else. The upstream's Secret is yours, so Geass leaves it.

| Field | Meaning |
| --- | --- |
| `enabled` | Runs the relay. |
| `upstream.host`, `.port` | The upstream. The port defaults to 587, or to 465 with `tls: TLS`. |
| `upstream.tls` | `StartTLS` (the default): the relay connects in plain text, requires STARTTLS, and refuses to send if the upstream does not offer it. `TLS`: TLS from the first byte. `None`: plain text, for an upstream inside the cluster or on a private network. `None` cannot be used with `credentialRef`, because the relay never sends the upstream's password in plain text. The upstream's certificate must be valid for `host`. |
| `upstream.credentialRef.name` | A Secret in `geass-system` with `username` and `password`. The relay signs in with AUTH PLAIN, over TLS only. It reads the Secret from a volume, so a new password is used within a minute or two, without a restart. If this is not set, the relay does not sign in. |
| `limits` | What each organization may send. See [Limits](#limits). |
| `allowedSenderDomains` | The domains organizations may send from. An organization's own `spec.mail.allowedSenderDomains` replaces this list. |
| `requireSenderDomain` | Refuses all mail from an organization that has no allowed sender domain, neither its own nor the platform's. Without it, such an organization may send from any address. |
| `deniedRecipientDomains` | The relay never sends to these domains, for any organization. A domain also covers the domains below it. |

## Choose an upstream

Any service that accepts mail by SMTP submission with a login will work. Check its documentation for the current host names and ports.

| Service | Host | Notes |
| --- | --- | --- |
| Amazon SES | `email-smtp.<region>.amazonaws.com` | Use SES's SMTP credentials, not an access key. Verify each organization's domain, and set up its DKIM in SES. A new account is in SES's sandbox and can only send to verified addresses. |
| Postmark | `smtp.postmarkapp.com` | The username and the password are both the server's API token. Ports 587 and 2525. |
| Mailgun | `smtp.mailgun.org` (or `smtp.eu.mailgun.org`) | Use an SMTP login of the sending domain. |
| Your own relay | Its address | Inside the cluster or on a private network, `tls: None` without a login is possible. The relay's network policy lets it reach the upstream's port on any address but the cloud's metadata. |

The relay sends every organization's mail through the one upstream account, so the upstream sees one sender. An organization that sends spam within its limits still harms that account's reputation. The limits bound how much harm it can do. They cannot prevent it.

## Limits

Each field of an organization's `spec.mail.limits` replaces the platform's `limits` for that field. A field that neither sets has the relay's default.

| Field | Default | At the limit |
| --- | --- | --- |
| `messagesPerMinute` | 60 | `450 4.7.1` at `MAIL FROM`: a temporary failure, so the sender tries again later |
| `messagesPerHour` | 600 | The same |
| `messagesPerDay` | 2000 | The same |
| `recipientsPerMessage` (1 to 1000) | 50 | `452 4.5.3` for the recipient after the limit, which tells the sender to send the rest in another message. The recipients already taken are sent to. |
| `maxMessageSize` (1Ki to 50Mi) | `10Mi` | `552 5.3.4`: when the message announces its size (`SIZE=`), or as soon as it grows past the limit. A message cut off this way is dropped at the upstream, because the relay closes that connection before the end of the data. |
| `connections` (1 to 1000) | 5 | `454 4.7.0` to the sign-in of one connection too many |

- **What counts.** Rates count messages, not recipients. A message to 50 people counts once, which is why recipients per message are capped too.
- **When it counts.** A message counts from the moment the relay takes its `MAIL` command, so that connections sending at the same time cannot all take the last free place. A message that is then not sent is given back. That includes a message that was refused, one that grew too large, and one the upstream did not take.
- **The windows** are sliding: any 60 seconds, any 60 minutes, any 24 hours. They are kept in small buckets, one second for the minute and one minute for the hour and the day. A message counts for at least the whole window and at most one bucket more. So no window ever holds more than the limit, and the message after the limit is refused exactly there.
- **Zero.** A rate of `0` sends nothing. Setting `messagesPerMinute: 0` on one organization stops its mail at once, without touching anyone else's.

**Sender domains.** The relay checks the envelope sender (`MAIL FROM`) and every address in the `From` and `Sender` headers. All of them must be at an allowed domain or below one: `acme.example` also covers `mail.acme.example`. A message without a `From` header is refused when senders are checked, and so is the empty sender (`MAIL FROM:<>`). The refusal is `550 5.7.1`. The relay only compares domains. Proving that a domain is the organization's is the upstream's job, through its domain verification and DKIM.

**Recipient domains.** A recipient at a denied domain, or below one, is refused with `550 5.7.1`. The other recipients of the same message are still taken.

Every answer the relay gives:

| Answer | When |
| --- | --- |
| `523 5.7.10` | `AUTH` before STARTTLS. The relay does not offer AUTH until TLS is on. |
| `530 5.7.0` | `MAIL` without signing in. |
| `535 5.7.8` | A wrong username or password. After three on one connection, the next attempt closes it. |
| `454 4.7.0` | Too many connections of the organization at once. |
| `421 4.7.0` | Too many connections altogether: 256, over every organization and every connection that has not signed in yet. |
| `450 4.7.1` | A rate limit. |
| `452 4.5.3` | Too many recipients. |
| `552 5.3.4` | Too large. |
| `550 5.7.1` | A sender the organization may not use, a recipient at a denied domain, or a login whose organization has been removed. |
| `451 4.4.1` | The upstream could not be reached, did not offer STARTTLS, or presented a certificate that is not valid for its name. |
| `451 4.7.0` | The upstream refused the relay's own login. This is the platform's to fix, so the client is told to try again. |
| `451 4.x.x` | The upstream deferred the message. |
| The upstream's `5xx` code | The upstream refused the message for good, such as an unverified sender. The relay passes on the code, but not the upstream's own words, which can name the platform's account. |
| `250` | The upstream took the message. |

## Where the counts are kept

**The counts live only in the memory of the relay's one replica.** This has consequences:

- **A restart starts every count from nothing.** The relay restarts when the operator is upgraded, when its node is drained, or when it runs out of memory. After a restart, an organization that had sent its whole day's limit can send that much again. If you need a firm daily cap, also set one at the upstream (SES has a sending quota for the whole account).
- **There is one replica, and there must only be one.** Two replicas would each count on their own, and each would let an organization send its full limit. Geass keeps the Deployment at one replica and puts it back if someone scales it. A new version replaces the old pod by stopping it first. So during an upgrade, or while the node is replaced, nothing answers on port 587 for a few seconds to a minute. Clients see a refused connection, and a mail library usually reports that as a failure: send from a job that tries again.
- **A change of settings does not restart the relay.** The relay reads its configuration and certificate again every few seconds from the Secrets that the kubelet keeps up to date, and it keeps what it has counted. A change reaches it within a minute or two. That includes a new organization, new limits, a rotated login and a renewed certificate.

`GET /api/v1/mail/usage` says when the current counts started (`countedSince`).

## How organizations use it

Each environment of each organization's project has a Secret `geass-mail`, including pull request environments once their namespace exists. A project of no organization gets none, and builds cannot reach the relay at all. Geass does not put the Secret into any pod by itself. A service that sends mail references it.

| Key | Value |
| --- | --- |
| `SMTP_HOST` | `geass-mail-relay.geass-system.svc` |
| `SMTP_PORT` | `587` |
| `SMTP_SECURITY` | `starttls` |
| `SMTP_USERNAME` | The organization's name, such as `org-1a2b3c4d` |
| `SMTP_PASSWORD` | The organization's password: 32 random bytes, base64url |
| `SMTP_CA_CERT` | The certificate of the authority that signed the relay's certificate (PEM) |

In the service's manifest, `envFrom` turns every key into an environment variable, and `secretRefs` also mounts the keys as files under `/secrets/refs/geass-mail/`:

```yaml
spec:
  envFrom:
    - secretRef:
        name: geass-mail
  secretRefs:
    - name: geass-mail
```

**How a client checks the relay.** The relay's certificate is signed by an authority that Geass keeps. It is not one of the public ones. Trust `SMTP_CA_CERT` as the client's certificate authority, connect with the name `SMTP_HOST`, and require STARTTLS. Do not turn certificate checks off: without them, anything that can take the relay's address can read the organization's password. For example, with Nodemailer:

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

The relay accepts AUTH `PLAIN` and `LOGIN`. Lines may be at most 2000 characters, as RFC 5321 expects. A longer line closes the connection.

## Networking

**Organizations reach the relay whatever their outbound limits say.** The limits close ports to addresses *outside* the cluster. The relay is a pod of the cluster, and an organization's pods reach the cluster's pods on every port (see [What Geass writes](/docs/guides/outbound-limits/#what-geass-writes)). This also holds with `allowedPorts`: it does not need to list 587, and no `allow` entry is needed for the relay. In the same way, a project with project isolation still reaches it, because isolation limits who reaches a project, not where a project's pods go. Builds reach nothing in the cluster but its DNS, so they cannot reach the relay.

**The relay's own policy**, `geass-mail-relay` in `geass-system`:

| Direction | Allowed |
| --- | --- |
| In, port 1587 (the Service's 587) | Every pod of the cluster. Each connection must still sign in. |
| In, port 9187 (health, metrics, usage) | `geass-system` (the dashboard) and `monitoring` (Prometheus). Metrics and usage also need the admin token. |
| Out | The cluster's DNS (`kube-system`, port 53), and the upstream's port on any address or pod, except the cloud's metadata range. Nothing else: not the cluster's API, which the relay never uses because its pod has no service account token. |

Policies hold only on a network plugin that enforces them. Without one, the submission port is still protected by the logins, and the admin port by the token. A dashboard that runs [outside the cluster](/docs/operations/dashboard-only/) cannot reach the admin port. To read mail usage from there, make the port reachable to it yourself, and set `GEASS_MAIL_RELAY_URL`.

## What it logs

The relay writes one JSON line per event, read with `kubectl -n geass-system logs deploy/geass-mail-relay`:

| Message | Fields |
| --- | --- |
| `signed in` | `org`, `remote` (the client's address) |
| `sign-in refused` | `org` (only when the username is an organization's, otherwise `unknown`), `remote`, `failures` |
| `message relayed` | `org`, `fromDomain`, `recipients` (how many), `bytes`, `durationMs` |
| A refusal (`a message over a rate limit was refused`, and others) | `org`, `reason`, `remote`, and the limit or domain concerned |
| `the upstream did not take a message` | `org`, `kind` (`unreachable`, `deferred`, `refused`, `auth`), `upstreamCode`, `recipients`, `durationMs` |
| `configuration loaded`, `certificate loaded`, `upstream credentials loaded`, `could not read the relay's ...` | What was read, and why it could not be |

The relay never logs a password, what a client sent to sign in, the upstream's login, the admin token, an address (only the sender's domain), a subject, a body, or the upstream's reply text. A test signs in correctly and wrongly, with a password typed where the username goes, sends through an upstream that requires a login, and then searches the whole log for each secret.

## Metrics and usage

With the monitoring add-on, Geass has Prometheus scrape the relay. It uses a ServiceMonitor `geass-mail-relay` in `monitoring`, with a copy of the admin token in the Secret `geass-mail-relay-metrics` beside it. The `org` label only ever holds the name of an organization the relay knows, or `unknown`.

| Metric | Labels | Meaning |
| --- | --- | --- |
| `geass_mail_relay_messages_accepted_total` | `org` | Messages the upstream took |
| `geass_mail_relay_recipients_accepted_total`, `geass_mail_relay_bytes_accepted_total` | `org` | Their recipients and bytes |
| `geass_mail_relay_rejections_total` | `org`, `reason` (`rate_minute`, `rate_hour`, `rate_day`, `recipients`, `size`, `sender_domain`, `recipient_domain`, `connections`, `organization_removed`) | What the relay refused |
| `geass_mail_relay_upstream_failures_total` | `org`, `kind` (`unreachable`, `deferred`, `refused`, `auth`) | Messages the upstream did not take |
| `geass_mail_relay_auth_failures_total` | `org` | Failed sign-ins |
| `geass_mail_relay_connections` | `org` | Connections signed in now |
| `geass_mail_relay_upstream_duration_seconds` | `result` | How long handing one message to the upstream took |
| `geass_mail_relay_config_reloads_total` | `result` (`ok`, `failed`) | Reading the configuration, certificate or upstream login again |

`GET /api/v1/mail/usage` (optionally with `?org=`) answers with each organization's counts. Only the platform's administrators can call it. See the [REST API](/docs/reference/rest-api/#mail-relay-usage).

## Rotate credentials

| What | How | Effect |
| --- | --- | --- |
| An organization's login | `kubectl -n geass-system annotate secret geass-mail-credentials-org-1a2b3c4d geass.dev/rotate-mail-credentials=now` | A new password goes into every `geass-mail` of the organization. The old password keeps working for 24 hours, so restart the services that send mail within that day: they read the Secret when they start. The organization gets the event `MailCredentialsRotated`. |
| Revoke a login at once | `kubectl -n geass-system delete secret geass-mail-credentials-org-1a2b3c4d` | A new password is made, and the old one stops working as soon as the relay reads its configuration again (a minute or two). Services fail to sign in until they restart. |
| Stop an organization's mail | `messagesPerMinute: 0` in its `spec.mail.limits` | Every message is refused with `450 4.7.1`, and its login is kept. |
| The upstream's password | Change the Secret that `credentialRef` names | Used within a minute or two, without a restart. |
| The relay's certificate | Nothing to do: it is renewed 30 days before its 90 days run out. To force a new one, delete `geass-mail-relay-tls`. | The authority stays the same, so clients need nothing new. |
| The authority | Delete `geass-mail-relay-ca` | A new authority is made (it lasts ten years and is replaced a year before it ends). Every `geass-mail` gets the new `SMTP_CA_CERT`, and the services that loaded the old one must restart. |
| The admin token | Delete the Secret `geass-mail-relay` | It is made again with a new token, which the dashboard and Prometheus read from it. |

## The condition

The [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/#mailrelayready) carries the condition `MailRelayReady` while the relay is set:

| Status | Reason | Meaning |
| --- | --- | --- |
| `True` | `Ready` | The relay's pod is ready. The message names the relay's address and the upstream. |
| `False` | `Starting` | The pod is not ready yet. |
| `False` | `UpstreamCredentialsMissing` | The Secret that `credentialRef` names is missing, or lacks `username` or `password`. The pod waits for it. |
| `False` | `ImageUnknown` | The operator's own Deployment could not be read for its image. |
| `False` | `InvalidSettings` | The settings cannot be applied, for example a `maxMessageSize` over 50Mi, or a login over plain text that the CRD did not catch. The relay keeps the configuration it had, and Geass records the `Warning` event `MailRelaySettingsInvalid` once. |

## What it does not do

- **It does not scan content.** There is no spam, malware or phishing filter. A message within the limits is sent.
- **It does not manage DKIM, SPF or DMARC.** Set up signing at the upstream (SES Easy DKIM, Postmark's DKIM) and publish the DNS records of each organization's domain yourself.
- **It does not handle bounces or complaints.** They go from the upstream to the envelope sender, and the relay never sees them. Suppression lists and complaint rates are the upstream's.
- **It has no queue and does not retry.** A message the upstream did not take is refused to the client. Retrying is the client's job.
- **It keeps no reputation per recipient or per destination**, and does not warm up new senders.
- **Its counts do not survive a restart, and it cannot run more than one replica.** See [Where the counts are kept](#where-the-counts-are-kept).
- **It has one upstream for every organization**, so they share that account's limits and reputation.
- **It is not reachable from outside the cluster**, and it does not offer TLS from the first byte (port 465). It offers only STARTTLS on 587.

## What has been tested

The relay is tested in-process. Go-smtp's client talks to it, as an organization's service would, and behind it is a fake upstream (another go-smtp server in the test). The tests check:

- TLS before sign-in, and the certificate checked by name and authority.
- Wrong, missing and foreign logins refused, and a connection closed after three failures.
- A rotated password taken until its time.
- Each rate refused exactly at its limit and taken again after its window.
- Recipients, size (announced and actual) and connections refused exactly at their limits.
- Organizations kept apart: one at its limit does not slow another.
- Sender domains checked in the envelope and the headers, and denied recipient domains refused.
- The upstream down, deferring, refusing a recipient or the message, and refusing the relay's login.
- STARTTLS and TLS to the upstream, with its certificate checked.
- Nothing answered before the upstream took the message.
- Exactly each organization's limit sent when many connections send at once, under the race detector.
- The log searched for every secret.

The controller is tested against fake clients and a real API server (envtest), from the platform's settings to the relay's Deployment, Service, policy, certificate, configuration and every organization's login and `geass-mail`:

- Changes, rotation and renewal.
- Projects moving between organizations.
- Edits made by hand, which are put back.
- Settings that cannot be applied.
- Nothing written when the relay is not set, and everything removed when it is turned off.
- A second pass that writes nothing.

The network policies are checked port by port with the same independent reading of NetworkPolicy rules that tests the outbound limits.

**Not tested:** the relay against a real mail service (SES, Postmark or any other), with a real mail program or library, or in a running cluster. The image, the volumes and the kubelet's updates to them have not been run together.

## Related

- [Outbound limits](/docs/guides/outbound-limits/)
- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/)
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/) and [GeassOrganization](/docs/reference/resources/geass-organization/)
- [Environment variables](/docs/guides/environment-variables/)
- [Security](/docs/operations/security/)
