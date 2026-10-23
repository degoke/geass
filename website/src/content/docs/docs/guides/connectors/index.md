---
title: "Cloud connectors"
description: "Connect AWS, PlanetScale, Cloudflare, GitHub and clouds that create machines in Settings, Connectors: what each is for, secrets, and troubleshooting."
sidebar:
  label: Overview
---

A connection is a credential that lets Geass call another service on your behalf. You manage them in **Settings** → **Connectors**, which has
three parts:

- **Code and domains**: [**GitHub**](/docs/guides/connectors/github/) (repository deploys) and [**Cloudflare**](/docs/guides/connectors/cloudflare/) (DNS and an
  optional tunnel for the dashboard). They belong to the whole platform. The button on a card reads **Configure**, and **Manage** once it is set up.
- **Server clouds**: [connections for machines](/docs/guides/connectors/clouds/), which let Geass create and delete servers at Amazon EC2, Azure,
  DigitalOcean, Google Cloud, Hetzner, Linode, Oracle Cloud, OVHcloud, Scaleway, UpCloud or Vultr, or through a webhook of your own. They belong to
  the cluster. This is experimental. **Connect a cloud** adds one (see [Servers and machines](#servers-and-machines)).
- **Storage and databases**: [**AWS**](/docs/guides/connectors/aws/) (S3 buckets and database backups) and
  [**PlanetScale**](/docs/guides/connectors/planetscale/) (a managed database). Each connection belongs to one project, or to an organization.

A project's link to GitHub is separate, under the project's **Settings** → **GitHub**.

![Settings, Connectors: the GitHub, Cloudflare, AWS and PlanetScale cards.](../../../../../assets/docs/connections.webp)
*Screenshot with example data, from before the cards were grouped under Code and domains and Storage and databases.*

:::caution
The connections for AWS buckets and PlanetScale databases are the least polished part of Geass. The credentials of a
connection that is only for storage or databases are not checked when you save them. The AWS and PlanetScale drawers list
their connections, with **Remove** on each and **Edit** on an AWS one. **Remove** refuses while anything still uses the connection: a pool or a machine, an object store or a
database, the backups of a database that is gone, a project's backup bucket or the control plane firewall. It names them,
so you can delete or move those first (see [Remove an AWS connection](/docs/guides/connectors/aws/#remove-an-aws-connection)).
This page documents what the code does today, including those rough edges, so you can work around them.
:::

## The connectors

| Connector | Used for | Scope | Credentials | Stored in |
| --- | --- | --- | --- | --- |
| [Clouds for machines](/docs/guides/connectors/clouds/) | Creating and deleting servers for [node pools](/docs/guides/node-pools/) and single servers | The whole cluster, never a project. Admins only | Each cloud's own: an API token, a pair of keys, a service account | Secret `<name>-credentials` in `geass-system` |
| [AWS](/docs/guides/connectors/aws/) | S3 buckets, project database backups | One project | Access key ID, secret access key, region | Secret `<name>-credentials` in `geass-system` |
| [PlanetScale](/docs/guides/connectors/planetscale/) | Creating a database and a login on it | One project | Service token, organization | Secret `<name>-credentials` in `geass-system` |
| [Cloudflare](/docs/guides/connectors/cloudflare/) | Dashboard DNS, optional tunnel, default hostnames | The whole cluster | API token, account ID, zone | Secret `platform-cloudflare` in `geass-system` |
| [GitHub](/docs/guides/connectors/github/) | Repository deploys, push webhooks, PR environments, commit statuses, deployments and pull request comments back to GitHub | The cluster, linked to each project | GitHub App credentials | Secret `platform-github-app` in `geass-system` |

Every connection is a Kubernetes Secret in the `geass-system` namespace. The installer turns on K3s secrets encryption,
so on a cluster it built they are encrypted in the cluster datastore, but anyone the API lets read Secrets still reads them.
Read [Security](/docs/operations/security/) before you give Geass a broad AWS key.

Only admins can add, change or remove connections. A viewer or an observer token does not get the AWS and PlanetScale
connections the dashboard sends, but can read the list of connections through the API (`GET /api/v1/connections`): each
connection's name, cloud, purposes, whether it works and what uses it, and never its credentials, the cloud account it belongs
to or what the cloud said about it (those are for admins). A project token cannot reach connections.
See [Users and API tokens](/docs/guides/users-and-tokens/).

## Servers and machines

There are two ways to get a server into the cluster, and a connection is needed for only one of them.

- **A server you have.** It joins over SSH, from a node resource that holds the server's address, an SSH user and a Secret with
  the private key. The user must be root or have passwordless `sudo`. Geass installs K3s on it and joins it to the
  cluster. This needs no connection. See [Add servers](/docs/getting-started/add-servers/) and [GeassNode](/docs/reference/resources/geass-node/).
- **A machine Geass creates.** With a [connection for machines](/docs/guides/connectors/clouds/), Geass creates a server at a cloud,
  installs K3s on it the same way, and deletes it when it is no longer wanted. Do it for one server (**Add server** on the **Usage**
  page, or `geass servers add`) or for a [pool](/docs/guides/node-pools/) that adds and removes machines as the load needs them.
  Geass can do this at Amazon EC2, Microsoft Azure, DigitalOcean, Google Cloud, Hetzner Cloud, Linode, Oracle Cloud, OVHcloud
  Public Cloud, Scaleway, UpCloud and Vultr, and through a webhook for any other provider. This is **experimental**: Amazon EC2 has
  run against a live account; the other drivers were written from each provider's documentation and tested against a simulation only. See
  [How far to trust it](/docs/guides/connectors/clouds/#how-far-to-trust-it).

A connection for machines is different from the others in four ways. It belongs to the cluster and never to a project. Only an admin
can use it. It is checked with the cloud when you save it, and again every 15 minutes. And the cloud bills the account it belongs to for
every machine Geass creates, so give it a project or an account of its own.

A cluster whose servers are at more than one provider is also experimental. See
[Multi-provider clusters](/docs/operations/multi-provider/).

:::caution
When Geass connects over SSH to join a server you brought it does not verify the server's host key unless you pin it with a
`known_hosts` entry in the SSH key Secret: see [Add servers](/docs/getting-started/add-servers/#pin-the-servers-host-key-recommended).
Without one, join only servers you trust the network path to, and see [Security](/docs/operations/security/). A machine Geass creates
is different: Geass makes its host key itself and checks it.
:::

## Where secrets are stored

| Connection | Secret | Keys |
| --- | --- | --- |
| AWS | `<name>-credentials` | `accessKeyId`, `secretAccessKey`, `region` |
| A cloud for machines | `<name>-credentials` | The cloud's own keys: see [the table](/docs/guides/connectors/clouds/#the-clouds) |
| The key Geass logs in to its machines with | `geass-nodepool-ssh` | `ssh-privatekey`, `ssh-publickey` |
| The host key of a machine Geass created | `<node>-hostkey` | `private`, `public` |
| PlanetScale | `<name>-credentials` | `token`, `organization` |
| Cloudflare | `platform-cloudflare` | `apiToken`, `accountId`, `zoneName`, `zones` |
| Cloudflare tunnel | `geass-cloudflared-token` | `token` |
| GitHub App | `platform-github-app` | `app_id`, `client_id`, `client_secret`, `slug`, `private_key`, `webhook_secret` |
| GitHub project install | `<project id>-github-token` | `token`, `installation_id`, `account`, `account_type` |
| AWS bucket user | `<bucket>-connection` in `<project>-<environment>` | `endpoint`, `accessKey`, `secretKey` and bucket details |
| Backup bucket user | `geass-backup-storage-<bucket>` | `ACCESS_KEY_ID`, `ACCESS_SECRET_KEY`, `REGION` |

Secret values never appear in a resource's `status`, and the dashboard never sends them to the browser. View the
Secrets with `kubectl -n geass-system get secret` (you need cluster access for that).

## What happens under the hood

- Each connection is a `GeassCloudConnection` (AWS, PlanetScale and the clouds for machines) in `geass-system`; Cloudflare and GitHub live on the
  platform configuration, named `platform`. See [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/).
- The connection controller sets `Ready` to `True` with reason `CredentialsReady`, or `False` with `CredentialsMissing` or
  `UnsupportedProvider`. For a connection with the purpose `Compute` it also asks the cloud whether it accepts the credentials, and
  a refusal is `False` with `VerificationFailed`. For the others it only checks that the Secret has what the provider needs.
- The object store, database and backup controllers read the connection and its Secret when they reconcile. They also
  check that the connection belongs to the resource's project.

```bash title="Inspect connections"
kubectl -n geass-system get geasscloudconnections
kubectl -n geass-system get geassplatformconfig platform -o yaml
kubectl -n geass-system get secret platform-cloudflare platform-github-app
```

## Troubleshooting

These apply to every connector that is saved for a project (AWS and PlanetScale). Errors specific to one connector are on
its own page: [AWS](/docs/guides/connectors/aws/#troubleshooting), [PlanetScale](/docs/guides/connectors/planetscale/#troubleshooting),
[Cloudflare](/docs/guides/connectors/cloudflare/#troubleshooting), [GitHub](/docs/guides/connectors/github/#troubleshooting) and
[clouds for machines](/docs/guides/connectors/clouds/#troubleshooting).

**The connection stays Pending.** The Secret is missing `accessKeyId` or `secretAccessKey` (AWS), or `token` or
`organization` (PlanetScale). The message says which: "AWS access key and secret key are required" or "PlanetScale token
and organization are required".

**"project was not found" when saving a connection.** You entered a display name. Use the project ID (`proj-` followed by
eight characters).

**"connection already exists".** A connection or resource with that name exists. Choose another name, and remember the
Secret `<name>-credentials` may still be there from an earlier attempt.

**"cloud connection X is scoped to project Y".** A bucket or database in project Z named a connection that belongs to
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
