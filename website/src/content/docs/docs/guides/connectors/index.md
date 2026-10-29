---
title: "Cloud connectors"
description: "Connect AWS, PlanetScale, Cloudflare, GitHub, and clouds that create machines in Settings, Connectors: what each is for, secrets, and troubleshooting."
sidebar:
  label: Overview
---

A connection is a credential that Geass uses to call another service for you. You manage connections in **Settings** → **Connectors**.
**Settings** → **Connectors** has three parts:

- **Code and domains**: [**GitHub**](/docs/guides/connectors/github/) (repository deploys) and [**Cloudflare**](/docs/guides/connectors/cloudflare/) (DNS and an
  optional tunnel for the dashboard). These connections belong to the whole platform. The button on a card shows **Configure**. After you set up the connection, the button shows **Manage**.
- **Server clouds**: [connections for machines](/docs/guides/connectors/clouds/). With these connections, Geass can create and delete servers at Amazon EC2, Azure,
  DigitalOcean, Google Cloud, Hetzner, Linode, Oracle Cloud, OVHcloud, Scaleway, UpCloud, or Vultr, or through a webhook of your own. These connections belong to
  the cluster. This feature is experimental. To add a connection, click **Connect a cloud**. For more information, see [Servers and machines](#servers-and-machines).
- **Storage and databases**: [**AWS**](/docs/guides/connectors/aws/) (S3 buckets and database backups) and
  [**PlanetScale**](/docs/guides/connectors/planetscale/) (a managed database). Each connection belongs to one project, or to an organization.

The link of a project to GitHub is separate. It is under **Settings** → **GitHub** of the project.

![Settings, Connectors: the GitHub, Cloudflare, AWS and PlanetScale cards.](../../../../../assets/docs/connections.webp)
*Screenshot with example data, from before the cards were grouped under Code and domains and Storage and databases.*

:::caution
The connections for AWS buckets and PlanetScale databases are the least polished part of Geass. Geass does not check the credentials of a
connection that is only for storage or databases when you save them. The AWS and PlanetScale drawers list
their connections. Each connection has **Remove**, and an AWS connection also has **Edit**.

**Remove** refuses while anything still uses the connection. These items can use a connection:

- A pool or a machine.
- An object store or a database.
- The backups of a database that is gone.
- The backup bucket of a project.
- The control plane firewall.

**Remove** names these items, so that you can delete or move them first. For more information, see [Remove an AWS connection](/docs/guides/connectors/aws/#remove-an-aws-connection).
This page documents what the code does today, with these rough edges included, so that you can work around them.
:::

## The connectors

| Connector | Used for | Scope | Credentials | Stored in |
| --- | --- | --- | --- | --- |
| [Clouds for machines](/docs/guides/connectors/clouds/) | The creation and deletion of servers for [node pools](/docs/guides/node-pools/) and single servers | The whole cluster, never a project. Admins only | The credentials of each cloud: an API token, a pair of keys, or a service account | Secret `<name>-credentials` in `geass-system` |
| [AWS](/docs/guides/connectors/aws/) | S3 buckets, project database backups | One project | Access key ID, secret access key, and region | Secret `<name>-credentials` in `geass-system` |
| [PlanetScale](/docs/guides/connectors/planetscale/) | The creation of a database and a login on it | One project | Service token, organization | Secret `<name>-credentials` in `geass-system` |
| [Cloudflare](/docs/guides/connectors/cloudflare/) | Dashboard DNS, optional tunnel, and default hostnames | The whole cluster | API token, account ID, and zone | Secret `platform-cloudflare` in `geass-system` |
| [GitHub](/docs/guides/connectors/github/) | Repository deploys, push webhooks, PR environments, and commit statuses, deployments, and pull request comments back to GitHub | The cluster, linked to each project | GitHub App credentials | Secret `platform-github-app` in `geass-system` |

Every connection is a Kubernetes Secret in the `geass-system` namespace. The installer turns on K3s secrets encryption.
So on a cluster that the installer built, the Secrets are encrypted in the cluster datastore. But anyone that the API lets read Secrets can still read them.
Read [Security](/docs/operations/security/) before you give Geass a broad AWS key.

Only admins can add, change, or remove connections. A viewer or an observer token does not get the AWS and PlanetScale
connections that the dashboard sends. But such a token can read the list of connections through the API (`GET /api/v1/connections`). For each connection, the list shows these values:

- The name, the cloud, and the purposes.
- Whether the connection works, and what uses it.

The list never shows the credentials, the cloud account that the connection belongs to, or what the cloud said about the connection.
Those values are for admins. A project token cannot reach connections.
For more information, see [Users and API tokens](/docs/guides/users-and-tokens/).

## Servers and machines

There are two ways to get a server into the cluster. Only one of the two ways needs a connection.

- **A server you have.** The server joins over SSH. Geass uses a node resource that holds the address of the server, an SSH user, and a Secret with
  the private key. The user must be root or have passwordless `sudo`. Geass installs K3s on the server, and joins it to the
  cluster. This way needs no connection. See [Add servers](/docs/getting-started/add-servers/) and [GeassNode](/docs/reference/resources/geass-node/).
- **A machine Geass creates.** With a [connection for machines](/docs/guides/connectors/clouds/), Geass creates a server at a cloud, and
  installs K3s on it in the same way. When the server is no longer wanted, Geass deletes it. You can do this for one server, with **Add server** on the **Usage**
  page or with `geass servers add`. You can also do this for a [pool](/docs/guides/node-pools/) that adds and removes machines as the load needs them.
  Geass can do this at Amazon EC2, Microsoft Azure, DigitalOcean, Google Cloud, Hetzner Cloud, Linode, Oracle Cloud, OVHcloud
  Public Cloud, Scaleway, UpCloud, and Vultr. For any other provider, Geass can do this through a webhook. This feature is **experimental**. The Amazon EC2 driver has
  run against a live account. The other drivers were written from the documentation of each provider, and only a simulation tested them. See
  [How far to trust it](/docs/guides/connectors/clouds/#how-far-to-trust-it).

A connection for machines is different from the other connections in four ways:

- It belongs to the cluster, and never to a project.
- Only an admin can use it.
- Geass checks it with the cloud when you save it, and again every 15 minutes.
- The cloud bills the account that the connection belongs to for every machine that Geass creates. So give the connection a project or an account of its own.

A cluster whose servers are at more than one provider is also experimental. See
[Multi-provider clusters](/docs/operations/multi-provider/).

:::caution
If you do not pin the host key, join only servers whose network path you trust. Geass connects over SSH to join a server that you brought.
Geass does not verify the host key of that server unless you pin the key with a `known_hosts` entry in the SSH key Secret. For more information, see [Add servers](/docs/getting-started/add-servers/#pin-the-servers-host-key-recommended)
and [Security](/docs/operations/security/). A machine that Geass creates is different: Geass makes its host key itself, and checks it.
:::

## Where secrets are stored

| Connection | Secret | Keys |
| --- | --- | --- |
| AWS | `<name>-credentials` | `accessKeyId`, `secretAccessKey`, `region`, and, for a connection for machines, `sessionToken` and `expiresAt` (both optional) |
| A cloud for machines | `<name>-credentials` | The keys of the cloud. See [the table](/docs/guides/connectors/clouds/#the-clouds) |
| The key that Geass uses to sign in to its machines | `geass-nodepool-ssh` | `ssh-privatekey`, `ssh-publickey` |
| The host key of a machine that Geass created | `<node>-hostkey` | `private`, `public` |
| PlanetScale | `<name>-credentials` | `token`, `organization` |
| Cloudflare | `platform-cloudflare` | `apiToken`, `accountId`, `zoneName`, `zones` |
| Cloudflare tunnel | `geass-cloudflared-token` | `token` |
| GitHub App | `platform-github-app` | `app_id`, `client_id`, `client_secret`, `slug`, `private_key`, `webhook_secret` |
| GitHub project install | `<project id>-github-token` | `token`, `installation_id`, `account`, `account_type` |
| AWS bucket user | `<bucket>-connection` in `<project>-<environment>` | `endpoint`, `accessKey`, `secretKey`, and bucket details |
| Backup bucket user | `geass-backup-storage-<bucket>` | `ACCESS_KEY_ID`, `ACCESS_SECRET_KEY`, `REGION` |

Secret values never appear in the `status` of a resource, and the dashboard never sends them to the browser. To view the
Secrets, run `kubectl -n geass-system get secret`. You need cluster access for this command.

## What happens under the hood

- Each AWS, PlanetScale, and cloud-for-machines connection is a `GeassCloudConnection` in `geass-system`. Cloudflare and GitHub are in the
  platform configuration, named `platform`. See [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/).
- The connection controller sets `Ready` to `True` with the reason `CredentialsReady`. Or it sets `Ready` to `False` with `CredentialsMissing` or
  `UnsupportedProvider`. For a connection with the purpose `Compute`, the controller also asks the cloud whether it accepts the credentials. If
  the cloud refuses, `Ready` is `False` with `VerificationFailed`. For the other connections, the controller only checks that the Secret has what the provider needs.
- The object store, database, and backup controllers read the connection and its Secret when they reconcile. These controllers also
  check that the connection belongs to the project of the resource.

```bash title="Inspect connections"
kubectl -n geass-system get geasscloudconnections
kubectl -n geass-system get geassplatformconfig platform -o yaml
kubectl -n geass-system get secret platform-cloudflare platform-github-app
```

## Troubleshooting

The problems in this section apply to every connector that is saved for a project (AWS and PlanetScale). The errors of one connector are on
the page of that connector: [AWS](/docs/guides/connectors/aws/#troubleshooting), [PlanetScale](/docs/guides/connectors/planetscale/#troubleshooting),
[Cloudflare](/docs/guides/connectors/cloudflare/#troubleshooting), [GitHub](/docs/guides/connectors/github/#troubleshooting), and
[clouds for machines](/docs/guides/connectors/clouds/#troubleshooting).

**The connection stays Pending.** The Secret does not have `accessKeyId` or `secretAccessKey` (AWS), or `token` or
`organization` (PlanetScale). The message says which: "AWS access key and secret key are required" or "PlanetScale token
and organization are required".

**"project was not found" when saving a connection.** You entered a display name. Use the project ID: `proj-` followed by
eight characters.

**"connection already exists".** A connection or a resource with that name exists. Use another name. Also, the
Secret `<name>-credentials` might still be there from an earlier attempt.

**"cloud connection X is scoped to project Y".** A bucket or a database in project Z named a connection that belongs to
another project. Add a connection for Z.

## Related

- [Cloud connections for machines](/docs/guides/connectors/clouds/)
- [Node pools and autoscaling](/docs/guides/node-pools/)
- [Connect AWS](/docs/guides/connectors/aws/)
- [Connect PlanetScale](/docs/guides/connectors/planetscale/)
- [Connect Cloudflare](/docs/guides/connectors/cloudflare/)
- [Connect GitHub](/docs/guides/connectors/github/)
- [Object storage](/docs/guides/object-storage/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Databases](/docs/guides/databases/)
- [GitHub and builds](/docs/guides/github-and-builds/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Security](/docs/operations/security/)
