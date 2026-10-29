---
title: "Cloud connections for machines"
description: "Let Geass create and delete machines at 11 clouds and a webhook: least-privilege credentials, regions, sizes, settings, and checks. Experimental."
sidebar:
  label: Clouds for machines
---

A connection for machines holds the credentials that Geass uses to create and delete servers at a cloud.
[Node pools](/docs/guides/node-pools/) use it, and so do servers that you add one at a time. Geass creates the
machine, installs K3s on it over SSH, and joins it to your cluster. This page tells you how such a connection works,
which credentials to make for each cloud, and what is special about each cloud.

Geass can create machines at these clouds: Amazon EC2, Microsoft Azure, DigitalOcean, Google Cloud, Hetzner Cloud,
Linode (Akamai Cloud), Oracle Cloud, OVHcloud Public Cloud, Scaleway, UpCloud, and Vultr. For any other provider,
Geass can use a webhook: an HTTP service of your own that follows a small contract.

:::caution
Before you give a connection credentials that can spend money, read [How far to trust it](#how-far-to-trust-it).
Machine creation is **experimental**. Amazon EC2 has run against a live account, in one region. The other eleven
drivers were written from the official API documentation of the provider. They were tested only against an in-memory
simulation of that API.
:::

## How a connection for machines works

- **It belongs to the cluster, never to a project.** A project must not be able to spend money on machines. So a
  connection that creates machines cannot be scoped to a project. Geass refuses a pool or a server that names a
  connection that is scoped to a project.
- **Only an admin can use it.** These actions need the admin role:
  - Make, change, and remove a connection.
  - List the sizes of a cloud.
  - Make a pool or a server with the connection.

  A viewer or observer can see the connection's name, its cloud, whether it works, and what uses it. A viewer or
  observer can never see its credentials, its cloud account, or what the cloud said about it. A project token cannot
  get to connections at all.
- **It has a purpose, `Compute`.** Every cloud on this page makes machines. So a connection to any of these clouds has
  the purpose `Compute`, unless you list other purposes. AWS is the one cloud that also holds
  [buckets and backups](/docs/guides/connectors/aws/). An AWS connection made before purposes existed is for `Storage`
  and `Database` only, and **never** starts to create machines. Make a separate AWS connection for machines, with its
  own key. See [Amazon EC2](#amazon-ec2). In practice, the two cannot be one connection: storage connections must
  belong to a project, and machine connections must not.
- **The credentials are stored in a Secret and never shown again.** The dashboard and the command line write the
  credentials into the Secret `<name>-credentials` in `geass-system`. Only the operator reads this Secret. To replace a
  credential, you must enter it again.
- **Geass asks the cloud whether it accepts them.** Geass asks at these times:
  - Immediately, and then every 15 minutes.
  - When you change the connection, or when you ask with `geass connections verify`.
  - Every 2 minutes, for a connection that does not work.

  If you put a new value into the Secret yourself, Geass detects it at the next of these checks.

### What the connection says about itself

`geass connections show NAME` and the **Server clouds** list show:

| What | Meaning |
| --- | --- |
| Status | `accepted`, `not accepted` (with the cloud's message), or `not checked yet`. The dashboard shows **Available** or **Not available**. |
| Account | The account, project, or tenant that the credentials act on, as the cloud reports it. |
| Last accepted | When the cloud last accepted the credentials. |
| Credentials expire | When the credentials stop working, for the clouds that give this date. Geass adds a warning from 14 days before. |
| Warnings | Things that work now and are likely to fail later. The section of each cloud lists its own warnings. |
| Used by | The pools and machines that depend on the connection. |

In Kubernetes, these are `status.available`, `status.account`, `status.message`, `status.verifiedAt`,
`status.expiresAt`, and `status.warnings` of the
[GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/). The `Ready` condition has these values:

- `True` when the cloud accepted the credentials.
- `False` with `VerificationFailed` when the cloud did not accept them.
- `False` with `CredentialsMissing` when the Secret does not have a required key.

The check reads from the cloud and changes nothing. For most clouds, the check cannot show that the credentials can
**create** a machine without creating one. The check tells you this in a warning. So the first machine is the real
test. See [How far to trust it](#how-far-to-trust-it). Only Amazon EC2 asks the cloud, with a dry run, whether the
cloud would allow a launch.

## The one rule for credentials: a boundary of their own

Give Geass a project, a resource group, a compartment, or an account that holds nothing else, whatever the cloud calls
it. Give Geass credentials that get only to that boundary. Geass lists and deletes the machines that have its labels,
and nothing else. But a boundary of its own makes that true even if something goes wrong:

- The credentials cannot touch what is outside the boundary.
- Everything inside the boundary is for Geass to delete.
- You get one bill to read.

The sections that follow tell you the boundary of each cloud and what the driver calls. Grant exactly that, and no
more. Where the driver's own text names a role or a scope, this page uses that name. Where it does not, this page
lists the calls, and the cloud's documentation gives the cloud's own action names. Do not take more than the calls
need.

## The clouds

| Cloud | ID | Credentials (a secret is marked) | What `region` means | Pool settings |
| --- | --- | --- | --- | --- |
| [Amazon EC2](#amazon-ec2) | `AWS` | `accessKeyId`, `secretAccessKey` (secret), `region` (optional), `sessionToken` (secret, optional), `expiresAt` (optional) | A region: `eu-north-1` | `subnetId`, `securityGroupId`, `diskGb` |
| [Microsoft Azure](#microsoft-azure) | `Azure` | `tenantId`, `clientId`, `clientSecret` (secret), `subscriptionId`, `resourceGroup` | A location: `westeurope` | `subnetId` (required), `nsgId`, `imageVersion`, `diskGb` |
| [DigitalOcean](#digitalocean) | `DigitalOcean` | `token` (secret) | A region slug: `fra1`. Empty lets DigitalOcean choose | `firewallId`, `vpcUuid` |
| [Google Cloud](#google-cloud) | `GCP` | `serviceAccountKey` (secret, JSON) | A zone: `europe-west4-a` | `network`, `subnetwork`, `image`, `diskSizeGb`, `diskType`, `firewallTag`, `skipFirewall` |
| [Hetzner Cloud](#hetzner-cloud) | `Hetzner` | `token` (secret) | A location: `fsn1`. Empty lets Hetzner choose | `firewallId`, `networkId` |
| [Linode](#linode-akamai-cloud) | `Linode` | `token` (secret) | A region that supports Metadata: `eu-west`. Required | `firewallId` |
| [Oracle Cloud](#oracle-cloud) | `OCI` | `tenancyOcid`, `userOcid`, `fingerprint`, `privateKey` (secret), `compartmentOcid` | A region: `eu-frankfurt-1` | `subnetId` (required), `availabilityDomain`, `imageId`, `nsgId`, `diskSizeGb` |
| [OVHcloud](#ovhcloud-public-cloud) | `OVHcloud` | `clientId`, `clientSecret` (secret), `serviceName`, `endpoint` (optional) | A Public Cloud region the project has enabled: `GRA11` | `imageId`, `networkId`, `userDataBase64` |
| [Scaleway](#scaleway) | `Scaleway` | `secretKey` (secret), `projectId` | A zone: `fr-par-1` | `securityGroupId`, `rootVolumeGb` |
| [UpCloud](#upcloud) | `UpCloud` | `token` (secret), or `username` and `password` (secret) | A zone: `de-fra1` | `diskGb` |
| [Vultr](#vultr) | `Vultr` | `apiKey` (secret) | A region id: `fra`. Required | `firewallGroupId` |
| [Your own service](#your-own-service-webhook) | `Webhook` | `url`, `token` (secret, optional) | Whatever your service takes | Any |

`geass providers` prints the same table from what the operator knows. `geass providers NAME` explains each credential
and setting. The dashboard's form has the same lists.

| Cloud | Spot machines | Price in `geass sizes` | Firewall Geass makes |
| --- | --- | --- | --- |
| Amazon EC2 | Yes | No | A security group `geass-<cluster>` |
| Microsoft Azure | Yes | Yes, from Azure's public price list (USD) | A network security group `geass-<cluster>-<location>` |
| DigitalOcean | No | Yes | A Cloud Firewall `geass-<cluster>` |
| Google Cloud | Yes | No | Two global firewall rules |
| Hetzner Cloud | No | Yes | A firewall `geass-<cluster>` |
| Linode | No | Yes | A Cloud Firewall |
| Oracle Cloud | Yes | No | None: you open the ports |
| OVHcloud | No | No | None: the cloud has no firewall API |
| Scaleway | No | Yes | A security group |
| UpCloud | No | Yes | A firewall on each server |
| Vultr | No | Yes | A firewall group |
| Webhook | Passed on | Whatever the service says | Whatever the service does |

"No" under spot means that the driver ignores `spot: true`. Where the cloud limits the length of a firewall's name,
Geass shortens the cluster name in it.

## Add a connection

### In the dashboard

1. Open **Settings**, and then **Connectors**.
2. Under **Server clouds**, click **Connect a cloud**.
3. Select the **Cloud**.
4. Enter a name for the connection. Use lowercase letters, numbers, and hyphens. Pools and servers refer to the
   connection by that name.
5. If you want a default region, enter the **Default region**.
6. Enter the credentials. A secret is a password box that the form never fills in again. For AWS, the form also
   offers **Buckets** and **Databases** next to the purpose Compute (shown as **Servers**), which it always has. Leave
   **Buckets** and **Databases** off, and make a separate connection for buckets.
7. Click **Connect**. Geass asks the cloud whether it accepts the credentials. The card shows **Available**, or the
   cloud's message.

After you connect, you can use these controls on the card:

- **Verify now** asks the cloud again.
- **Edit** changes the default region or replaces credentials. Enter only the credentials that change.
- **Remove** deletes the connection and its Secret.

### With the command line

```bash
geass providers Hetzner                     # what it takes
geass connections add hetzner --provider Hetzner --credential-file token=./hetzner-token.txt
```

The command makes the connection, asks the cloud immediately, and prints what the cloud said, including its warnings.

Do not put a secret on the command line. The shell's history and the process list keep it. Give the secret in one of
these ways:

- From a file, with `--credential-file KEY=PATH`. The value `-` reads standard input.
- In the environment, as `GEASS_CREDENTIAL_` and the key in capitals, with an underscore at each capital. For
  example, `GEASS_CREDENTIAL_TOKEN` or `GEASS_CREDENTIAL_ACCESS_KEY_ID`.

A key of several lines must come from a file, for example a Google Cloud service account key or an Oracle Cloud
private key. If a required secret is still missing and a person is at the keyboard, the command asks for it, and does
not show it.

```bash
geass connections add aws-compute --provider AWS --region eu-north-1 \
  --credential accessKeyId=AKIA... --credential-file secretAccessKey=./secret.txt
geass connections list
geass connections show hetzner
geass connections update hetzner --credential-file token=./new-token.txt   # rotate
geass connections verify hetzner
```

This command makes only connections for machines. To make a connection for buckets or databases, use the dashboard.
See [`geass connections`](/docs/reference/cli/#geass-connections).

### With a manifest

```yaml title="hetzner-connection.yaml"
apiVersion: v1
kind: Secret
metadata:
  name: hetzner-credentials
  namespace: geass-system
type: Opaque
stringData:
  token: replace-with-the-api-token
---
apiVersion: geass.geass.dev/v1alpha1
kind: GeassCloudConnection
metadata:
  name: hetzner
  namespace: geass-system
spec:
  provider: Hetzner
  secretRef:
    name: hetzner-credentials
  purposes: [Compute]        # left out, a connection to any cloud but AWS and PlanetScale is for Compute
  region: fsn1               # optional default region
```

The keys of the Secret are the credential names in the table earlier on this page. Do not set `spec.project`. After
you change a Secret by hand, ask again with `geass connections verify hetzner`.

## Choose a region and a size

```bash
geass sizes --connection hetzner --region fsn1 --min-vcpus 2 --min-memory 4 --limit 10
```

This command prints the sizes that the cloud offers in that region. The cheapest size is first. Where the cloud
states no price, the smallest size is first. For each size, the command shows the processors, memory, disk,
architecture, and the price for an hour in the cloud's currency.

Geass asks the cloud with the connection's credentials, so this command needs an admin. A node reserves about 250m CPU
and 700 MiB of memory for K3s and the system pods. So a size under 2 vCPU and 4 GiB leaves little. The `ARCH` column
shows `amd64` or `arm64`. A node of one architecture can run only images built for that architecture.

## Amazon EC2

**Boundary.** An IAM user that only Geass uses. Put the user in an AWS account that holds nothing that you cannot
lose, or limit the user with conditions. Make this connection separate from any connection that you use for buckets,
and keep the key separate. The key that creates buckets and IAM users must not also launch instances.

**What the driver calls.** The driver makes these calls:

- `sts:GetCallerIdentity`, which needs no permission.
- The reads `DescribeInstances`, `DescribeInstanceTypes`, `DescribeImages`, `DescribeSubnets`, and
  `DescribeSecurityGroups`.
- `RunInstances`, and a `DryRun` of it to check that the key can launch.
- `CreateTags`, through the tags that it puts on a launch.
- `TerminateInstances`.
- For the security group that it makes: `CreateSecurityGroup`, `AuthorizeSecurityGroupIngress`, and
  `RevokeSecurityGroupIngress`.

A launch tags the instance and its volume `geass-managed=true`, `geass-cluster`, `geass-pool`, and `geass-node`.

```json title="geass-compute-policy.json"
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "ReadWhatGeassChoosesFrom",
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeInstances",
        "ec2:DescribeInstanceTypes",
        "ec2:DescribeImages",
        "ec2:DescribeSubnets",
        "ec2:DescribeSecurityGroups"
      ],
      "Resource": "*"
    },
    {
      "Sid": "LaunchOnlyInstancesTaggedGeassManaged",
      "Effect": "Allow",
      "Action": "ec2:RunInstances",
      "Resource": "arn:aws:ec2:*:*:instance/*",
      "Condition": { "StringEquals": { "aws:RequestTag/geass-managed": "true" } }
    },
    {
      "Sid": "LaunchUsingTheseResources",
      "Effect": "Allow",
      "Action": "ec2:RunInstances",
      "Resource": [
        "arn:aws:ec2:*::image/*",
        "arn:aws:ec2:*:*:subnet/*",
        "arn:aws:ec2:*:*:security-group/*",
        "arn:aws:ec2:*:*:network-interface/*",
        "arn:aws:ec2:*:*:volume/*"
      ]
    },
    {
      "Sid": "TagWhatALaunchCreates",
      "Effect": "Allow",
      "Action": "ec2:CreateTags",
      "Resource": ["arn:aws:ec2:*:*:instance/*", "arn:aws:ec2:*:*:volume/*"],
      "Condition": { "StringEquals": { "ec2:CreateAction": "RunInstances" } }
    },
    {
      "Sid": "TerminateOnlyWhatCarriesTheTag",
      "Effect": "Allow",
      "Action": "ec2:TerminateInstances",
      "Resource": "arn:aws:ec2:*:*:instance/*",
      "Condition": { "StringEquals": { "aws:ResourceTag/geass-managed": "true" } }
    },
    {
      "Sid": "MakeTheClustersSecurityGroup",
      "Effect": "Allow",
      "Action": "ec2:CreateSecurityGroup",
      "Resource": ["arn:aws:ec2:*:*:security-group/*", "arn:aws:ec2:*:*:vpc/*"]
    },
    {
      "Sid": "TagTheSecurityGroup",
      "Effect": "Allow",
      "Action": "ec2:CreateTags",
      "Resource": "arn:aws:ec2:*:*:security-group/*",
      "Condition": { "StringEquals": { "ec2:CreateAction": "CreateSecurityGroup" } }
    },
    {
      "Sid": "ChangeTheRulesOfGroupsGeassMade",
      "Effect": "Allow",
      "Action": ["ec2:AuthorizeSecurityGroupIngress", "ec2:RevokeSecurityGroupIngress"],
      "Resource": "arn:aws:ec2:*:*:security-group/*",
      "Condition": { "StringEquals": { "aws:ResourceTag/geass-managed": "true" } }
    }
  ]
}
```

Notes on the policy:

- In EC2, `Describe` calls take no resource-level conditions, so they are open (`"Resource": "*"`). So a key that can
  launch only instances tagged `geass-managed=true` cannot list only those instances. Geass narrows the list with a
  tag filter in each request.
- To limit the key to the regions that you use, add one more statement. Do not add a condition on every statement.
  The policy in this section is 1,708 bytes without spaces, and an inline user policy can hold only 2,048 bytes. So
  conditions on all eight statements do not fit. A managed policy can hold 6,144 bytes.

  ```json
  {
    "Sid": "OnlyInEuNorth1",
    "Effect": "Deny",
    "Action": "ec2:*",
    "Resource": "*",
    "Condition": { "StringNotEquals": { "aws:RequestedRegion": "eu-north-1" } }
  }
  ```
- Add this statement only if you use
  [`controlPlaneFirewall`](/docs/guides/node-pools/#let-geass-open-the-control-plane) with the control plane's own
  security group. Geass adds and removes rules on that group, and this statement lets Geass do that. The key can be
  this key, or the key of another connection at the same account. Geass marks each rule with the description `geass`:

  ```json
  {
    "Sid": "OpenTheControlPlanesGroup",
    "Effect": "Allow",
    "Action": ["ec2:AuthorizeSecurityGroupIngress", "ec2:RevokeSecurityGroupIngress"],
    "Resource": "arn:aws:ec2:eu-north-1:123456789012:security-group/sg-0abc123"
  }
  ```
- With `spot`, the account also needs what AWS requires for Spot machines: the Spot service-linked role. The driver
  makes no call for this role, so this policy does not cover it. If AWS refuses a spot launch, the error names the
  missing action.
- This policy has run against a live account in `eu-north-1`. It ran as an IAM user's key with exactly these
  statements, the region guard in this section, and the control plane statement. With this key, Geass did these
  actions:
  - Launched, tagged, and terminated machines.
  - Made and changed the cluster's security group.
  - Listed sizes.
  - Opened and closed rules on the control plane's group.

  A call that the policy refuses shows as the node's message, and the message names the action. An organization's
  service control policy can also refuse a launch, whatever the key's policy says. The message names that policy.

**Region and sizes.** An AWS region, for example `eu-north-1`. A pool's `region` has priority, then the connection's
default, then `us-east-1`. Sizes are the current-generation instance types that are not bare metal, with 1 to 32
vCPUs. The EC2 API states no prices. So Geass lists the sizes smallest first, with no price.

**Settings.** These are the settings:

- `subnetId` launches into that subnet. The subnet must give instances a public address, or the pool's `addressType`
  must be `Private`. Without `subnetId`, Geass uses a default-VPC subnet.
- `securityGroupId` uses a group that you made, as it is, instead of the group that Geass makes.
- `diskGb` sets the root disk (default 30, at least 8).

**Worth knowing.**

- The security group is `geass-<cluster>`. Geass makes it one time in the VPC of the subnet. The group lets in SSH and
  the kubelet port from the control plane's addresses. In a multi-site cluster, it also lets in the overlay's UDP port
  from anywhere. Geass keeps those three rules equal to what the cluster needs, and does not change rules on other
  ports.
- Instances accept only IMDSv2 and one network hop, so pods cannot read the instance's credentials.
- Geass launches burstable instances (the `t` families) with standard credits. The default, unlimited, can bill more
  than the hourly price.
- The image is the newest Canonical Ubuntu 24.04 for the architecture of the instance type.
- EC2 shows a machine a moment after you launch it. EC2 lists a terminated machine for about an hour. Geass waits, and
  ignores the terminated machines.
- EC2 deletes a root volume with the instance, and also the address that EC2 assigned.

### Temporary credentials

Instead of an IAM user's key, the connection can hold the credentials that `sts:AssumeRole` returns:

- An access key ID that starts with `ASIA`.
- A secret access key.
- A **session token**.

Put the token in the optional secret credential `sessionToken`. Geass then sends the token with every request to EC2
and STS, as the header `X-Amz-Security-Token`, and signs it with the request. Without a `sessionToken`, nothing
changes: Geass signs requests as it always did. The role needs the permissions of the policy earlier on this page.
Only a connection for machines reads the token. A connection for
[buckets and backups](/docs/guides/connectors/aws/) uses an IAM user's key.

Temporary credentials expire after an hour, unless the role sets a different time. So the system that issues them
must replace them before they expire. Replace the key, the secret, and the token together, in one request. If you
want the connection to show when the credentials expire, send `expiresAt` with them:

```bash title="Replace temporary credentials"
curl -fsS -X PATCH https://geass.example.com/api/v1/connections/aws-compute \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Content-Type: application/json" \
  -d '{"accessKeyId": "ASIA...", "secretAccessKey": "...", "sessionToken": "...", "expiresAt": "2026-10-22T14:30:00Z"}'
```

`geass connections update aws-compute --credential-file KEY=PATH` does the same. Give one `--credential-file` for each
key.

- **A new key is used immediately.** The operator builds the driver for a connection from its Secret. The operator
  builds the driver again when the Secret changes. So the next machine that Geass makes, reads, or deletes uses the
  new credentials. Nothing restarts. Geass refreshes the connection's own status (Available, the account,
  Credentials expire) at the next check, within 15 minutes. To refresh it immediately, use
  `geass connections verify aws-compute` or `POST /api/v1/connections/aws-compute/verify`.
- **Credentials that lapse before new ones arrive** make AWS answer `ExpiredToken`. The connection shows AWS's message
  and is not available. Geass does not treat this answer as credentials that are refused permanently, which would fail
  the node. Instead, these things occur:
  - A machine waits to be made or deleted, with AWS's message as the node's message. It continues when new
    credentials arrive.
  - Geass does not replace a running node in the meantime. Geass cannot ask AWS about the node's machine, so Geass asks
    again a minute later.
  - A machine that Geass cannot delete continues to bill until new credentials arrive.
- **AWS does not say when credentials end.** `GetCallerIdentity` has no expiry. So Geass shows an expiry only if you
  send `expiresAt`, an RFC 3339 time, for example `2026-10-22T14:30:00Z`. The expiry shows as Credentials expire
  (`status.expiresAt`). With a session token, Geass adds no "expires in N days" warning. Credentials that are renewed
  every hour would show that warning all the time. An `expiresAt` that is not a time gives a warning, and changes
  nothing else. On a key with no session token, `expiresAt` works as it does for any cloud, with the warning from 14
  days before.
- **A key or token that was set cannot be cleared through the API**. A field that you leave out or send empty keeps
  its value. To go back from temporary credentials to an IAM user's key, make a new connection. Alternatively, remove
  `sessionToken` and `expiresAt` from the Secret `<name>-credentials` with `kubectl`.

## Microsoft Azure

**Boundary.** A resource group that only Geass uses, and a custom role scoped to that group. Azure cannot scope a role
by tag, so the group is the boundary.

**Credentials.** A Microsoft Entra app registration (service principal) with a client secret, and then the five fields
in the table. Give the service principal a custom role on the resource group with these permissions, and no more:

- Virtual machines, disks, network interfaces, public IP addresses, and network security groups, in the group.
- The join action on the subnet that the pool uses. The subnet is in a virtual network that Geass does not manage.
- The read of VM sizes at the subscription (`Microsoft.Compute/locations/vmSizes/read`). A role scoped to the group
  does not include this permission.

Look up the exact action names in Azure's documentation. The connection warns if it cannot read the sizes.

**What the driver calls.** The driver does these actions:

- It reads the resource group, lists the group's virtual machines, and reads the subscription's VM sizes for a
  location.
- It creates a machine with one PUT of the virtual machine. The PUT describes the network interface, the public
  address, and the OS disk, and Azure makes them. Each has its delete option set to `Delete`. So when Geass deletes
  the machine, Azure deletes them too, and nothing continues to bill.
- It reads the interface and the public address back, and reads and writes network security groups.
- It reads prices from Azure's public price list, and sends no credential there.

**Region and sizes.** An Azure location name, for example `westeurope`, `northeurope`, `uksouth`, `eastus2`, or
`swedencentral`. Sizes come from the subscription's list for the location, with prices in US dollars from the public
price list. Azure gives no architecture in that list. So Geass takes a size whose name has a `p` in its suffix, for
example `Standard_D4ps_v5`, to be ARM, and gives it the arm64 image.

**Settings.** These are the settings:

- `subnetId` is required. It is the full resource ID of an existing subnet in the pool's region.
- `nsgId` uses your own network security group. This group must let in what the cluster needs.
- `imageVersion` pins the version of the Ubuntu image (default `latest`).
- `diskGb` is the OS disk (default 30).

The pool's `image` can be `publisher:offer:sku:version`, or the resource ID of a gallery image or a custom image.

**Worth knowing.**

- **The client secret expires**, after 24 months or less. Azure does not tell a client secret how long it has left.
  When the secret expires, every call fails with `AADSTS7000222`. Record the date, and renew the secret before then
  (`geass connections update NAME --credential-file clientSecret=PATH`). The connection warns about this, but it
  cannot count down.
- **Azure cannot say whether the role is complete** without doing something. A role can lack the right to create, to
  join the subnet, or to write security groups. Such a role passes the connection check, and fails when Geass makes
  the first machine.
- Public addresses are Standard SKU and static. They accept nothing until a network security group allows it. So every
  machine has a network security group: yours, or the group that Geass makes, with the name
  `geass-<cluster>-<location>`. Geass's group lets in SSH and the kubelet port from the control plane. In a multi-site
  cluster, it also lets in the overlay port from anywhere. Then it
  **denies everything else coming in, including the virtual network's default allow**. Other machines in the same
  virtual network can get to the new machine only through those rules.
- A machine of the same name that belongs to another cluster is an error. Geass never touches that machine.
- Geass shows a machine whose provisioning fails as stopped, and deletes it like any other machine. So an allocation
  failure that Azure reports only later shows as a machine that never runs.

## DigitalOcean

**Boundary.** A Team that only Geass uses.

**Credentials.** A personal access token with these custom scopes:

- `droplet` (create, read, delete).
- `tag` (create, read).
- `firewall` (create, read, update).
- `image:read`, `vpc:read`, `sizes:read`, and `regions:read`.

A full-access token also works. If you want Geass to show the Droplet limit and the account's status, add
`account:read`. Without this scope, a warning tells you so.

**Region and sizes.** A region slug: `nyc1`, `nyc3`, `sfo3`, `tor1`, `ams3`, `lon1`, `fra1`, `sgp1`, `blr1`, or
`syd1`. Empty lets DigitalOcean choose. Sizes are Droplet slugs, for example `s-2vcpu-4gb`, all amd64. Geass leaves
out GPU sizes, because Geass would never use the GPU.

**Settings.** `firewallId` attaches each machine to your own Cloud Firewall (its UUID), and Geass changes nothing in
it. `vpcUuid` creates the machine in that VPC instead of the region's default.

**Worth knowing.**

- DigitalOcean limits the number of Droplets that an account can have. The connection warns when the account is at
  its limit. Before you set a large `maxNodes`, ask DigitalOcean to raise the limit. The limit and a token that does
  not have a scope both give errors that look the same. Geass tells them apart by the message.
- Droplets have tags, not labels. So Geass writes its labels as tags, for example `geass-pool:burst`. Geass makes one
  Cloud Firewall for each cluster, `geass-<cluster>`, aimed at a tag. So the firewall covers a machine from the moment
  that the machine exists. The firewall allows every outbound connection. A firewall with no outbound rule would
  block all outbound traffic.
- A Droplet's name is not unique, and DigitalOcean has no idempotency token. So Geass looks for the node's Droplet by
  tag and name before it makes another.
- When Geass deletes a Droplet, DigitalOcean deletes only the Droplet. Geass makes no snapshots, backups, volumes, or
  reserved addresses, so nothing else bills.
- The API allows 250 requests a minute. Geass waits when the API tells it to slow down.
- The check cannot see a token's scopes. A token without `droplet:create`, `tag:create`, or `firewall:create` passes
  the check, and fails when Geass makes the first machine.

## Google Cloud

**Boundary.** A project that only Geass uses.

**Credentials.** A service account JSON key from that project. To get the key, go to IAM, Service accounts, Keys, Add
key, JSON. Paste the whole file, or give it with `--credential-file serviceAccountKey=PATH`. Give the service account
`roles/compute.instanceAdmin.v1` and `roles/compute.securityAdmin`. The first role cannot make firewall rules.

**Region and sizes.** For Google Cloud, a region is a **zone**: `europe-west4-a`, `us-central1-b`,
`asia-southeast1-a`. Machine types and stock are different in each zone. The Compute Engine API states no prices, so
Geass lists the sizes smallest first.

**Settings.** These are the settings:

- `network` and `subnetwork` select where to put the machine, as a name or a full path. A subnetwork is required when
  the network uses custom subnets.
- `image` is a source image or an image family. The default is Ubuntu 24.04 LTS for the architecture of the machine
  type.
- `diskSizeGb` (default 30) and `diskType`.
- `firewallTag` names a network tag that your own firewall rules already target. Geass then makes no rules.
- `skipFirewall` set to `true` makes no rules and adds no tag. You must then open tcp 22 and 10250 and udp 51820
  yourself.

**Worth knowing.**

- **Organizations created since May 2024 refuse to create service account keys** (the
  `iam.disableServiceAccountKeyCreation` policy). An administrator might have to remove this policy for the project
  before the key can exist.
- **`roles/compute.instanceAdmin.v1` does not grant `compute.firewalls.create` or `update`.** Without
  `roles/compute.securityAdmin`, or the `skipFirewall` or `firewallTag` setting, machine creation fails. The error names
  the permission. Geass does not make a machine that nothing can get to. Google cannot say whether the key can make
  firewall rules without making one. So the connection always has that warning.
- Geass makes two global firewall rules, aimed at the network tag `geass-<cluster>` that Geass puts on the machine:
  - `geass-<cluster>`: SSH and the kubelet.
  - `geass-<cluster>-overlay`: the overlay's UDP port. This rule is switched off unless the cluster is multi-site.

  Rules have no labels. So Geass recognizes its rules by their description, which starts with "Managed by Geass".
- Geass attaches no service account to the machine, so the machine cannot get to other Google APIs. Google Cloud
  deletes the boot disk and the ephemeral external address with the machine. Geass switches off OS Login for the
  machine. So if a project turns on OS Login, this does not stop the login that Geass makes through cloud-init.
- Compute Engine reports a quota or a stock-out only inside the long-running operation of the insert. So Geass watches
  that operation for a few seconds and returns the failure.

## Hetzner Cloud

**Boundary.** A Hetzner Cloud Project that only Geass uses. A token belongs to one Project, so the token cannot touch
anything outside that Project.

**Credentials.** An API token with **Read & Write** permission. To make the token, go to Project, Security, API tokens.

**Region and sizes.** A location: `fsn1`, `nbg1`, `hel1` (Europe), `ash`, `hil` (US), or `sin` (Singapore). Empty
lets Hetzner choose. Sizes are server types, for example `cx23`, with the price for each location.

**Settings.** `firewallId` uses your own firewall instead of the firewall that Geass makes. `networkId` also attaches
the machine to that private network.

**Worth knowing.**

- A read-only token passes the connection check, and fails when Geass makes the first machine. Hetzner cannot tell
  the two token types apart without creating something. The connection warns about this.
- Geass makes a firewall `geass-<cluster>` one time, and keeps its rules correct:
  - SSH and the kubelet port from the control plane. If Geass knows no control plane address, SSH from anywhere.
  - In a multi-site cluster, the overlay's UDP port from anywhere.

  The firewall drops all other incoming traffic.
- The machine gets a public IPv4 address and no IPv6. When Geass deletes a server, Hetzner releases its address.
- Hetzner is one of the two clouds that
  [`controlPlaneFirewall`](/docs/guides/node-pools/#let-geass-open-the-control-plane) works for. Geass adds one rule
  for each protocol and port, with the description `geass-access`, to the firewall that you name. When the machine
  goes, Geass removes the machine's address from the firewall.

## Linode (Akamai Cloud)

**Boundary.** An account that only Geass uses.

**Credentials.** A personal access token with read/write on Linodes, Firewalls, and IPs. To make the token, go to
Profile, API Tokens.

**Region and sizes.** A region ID that supports Metadata, for example `us-east`, `us-ord`, `us-iad`, `us-lax`,
`eu-west`, `eu-central`, `ap-south`, `ap-northeast`, or `ap-southeast`. A region is required, in the pool or in the
connection. Sizes are Linode types, for example `g6-standard-2`. The list leaves out the types that a region does not
sell, and GPU types.

**Settings.** `firewallId` attaches your own Cloud Firewall (its number), and Geass changes nothing in it.

**Worth knowing.**

- The machine's cloud-init document goes in the Linode's Metadata user data. Only some regions support Metadata.
  Geass reads the capabilities of the region. If the region does not have `Metadata`, Geass fails with a plain
  message. Geass does not make a machine that never gets its key.
- Geass asks for legacy configuration interfaces, because it needs them to attach a firewall at creation. An account
  whose `interfaces_for_new_linodes` setting is `linode_only` refuses this request. The create then fails with a
  message that tells you what to change. Geass does not read that setting, so the connection always has a warning
  about it.
- To create from an image, Linode needs a root password. Geass sends a random password that it never stores or shows.
  The login is the key in cloud-init, which turns password login off.
- Labels go as tags, and Geass splits a long label over several tags. Geass cleans up the Linode's label to Linode's
  rules.
- Billing is hourly, rounded up. When Geass deletes a Linode, Linode releases its addresses and deletes its disks.
- The check cannot see a token's scopes. A token that cannot write Linodes or Firewalls passes the check.

## Oracle Cloud

**Boundary.** A compartment that only Geass uses.

**Credentials.** An API signing key. You need these values:

- A user's RSA key **without a passphrase**.
- That user's tenancy OCID and user OCID.
- The key's fingerprint (`aa:bb:cc:...`).
- The compartment's OCID (`ocid1.compartment...`, or `ocid1.tenancy...` for the root).

The user must have these permissions:

- Manage instance-family in that compartment.
- Use virtual-network-family in that compartment.
- List the tenancy's availability domains. If you give the `availabilityDomain` setting, the user does not need this
  permission.

The clock of the machine that the operator runs on must be within five minutes of Oracle's clock. A skewed clock makes
every request a 401.

**Region and sizes.** A region identifier: `eu-frankfurt-1`, `us-ashburn-1`, `uk-london-1`, or `ap-sydney-1`. Geass
supports only the commercial realm. For a fixed shape, a size is the shape's name (`VM.Standard2.2`). For a Flex shape,
a size is `SHAPE:OCPUS:MEMORY_GB`, for example `VM.Standard.E4.Flex:2:8`. An OCPU is two vCPUs on x86 and one vCPU on
Arm. `geass sizes` reports vCPUs. The Oracle API states no prices.

**Settings.** These are the settings:

- `subnetId` is required. It is the subnet to attach the machine to, and it must allow a public address.
- `availabilityDomain` is a name, for example `Uocm:EU-FRANKFURT-1-AD-1`. By default, Geass tries the tenancy's first
  availability domain. When that domain has no capacity, Geass tries the others.
- `imageId`.
- `nsgId`: network security groups, separated by commas.
- `diskSizeGb` (at least 50).

**Worth knowing.**

- **Geass does not touch the network.** The subnet's security list, or the network security group in `nsgId`, must let
  in tcp 22 and 10250 from the control plane, and udp 51820 in a multi-site cluster. If not, Geass makes machines that
  never join. The connection tells you this in a warning.
- "Out of host capacity" is common in some regions. It is a stock-out in one availability domain. A pool with
  fallbacks moves on after three minutes. See
  [Fallback sizes and regions](/docs/guides/node-pools/#fallback-sizes-and-regions).
- Oracle's Ubuntu images have an operating system firewall that lets in SSH and ends with a reject. The first-boot
  script opens udp 51820 and tcp 10250 with `iptables`, before the reject, and saves the rules.
- Oracle bills per second, with a one-minute minimum. Oracle keeps a terminated instance in its list for about twelve
  hours. Geass sees that instance as gone.
- Launches are idempotent through a retry token. Oracle honors the token for 24 hours.

## OVHcloud Public Cloud

**Boundary.** A Public Cloud project that only Geass uses. This is the **Public Cloud** product, the product whose
instances you can create and delete by the hour through the API. The VPS product cannot autoscale, and Geass does not
use it.

**Credentials.** You need these values:

- An OAuth2 service account: `clientId` and `clientSecret`. Make it with `POST /me/api/oauth2/client`, flow
  `CLIENT_CREDENTIALS`. OVHcloud shows the secret one time only.
- The project's ID (`serviceName`, 32 hexadecimal characters).
- The `endpoint` where your OVHcloud account is: `eu` (the default), `ca`, or `us`.

The service account has no rights until an IAM policy gives it rights on the project. The service account must be able
to list flavors, images, and networks, and to create, read, and delete instances.

**Region and sizes.** A Public Cloud region that the project has enabled: `GRA11`, `SBG5`, `RBX-A` (Europe), `BHS5`
(Canada), `WAW1`, `DE1`, `UK1`, `SGP1`, or `SYD1`. **Enable the region in the project first**. Geass never enables a
region, because that is your decision. A size is the flavor's name, for example `d2-4`, `b3-8`, or `c3-16`. Each
region has its own flavor IDs, and Geass looks them up from the name. The calls that Geass makes return no prices.

**Settings.** These are the settings:

- `imageId`. Image IDs are different in each region. The default is the region's Ubuntu 24.04.
- `networkId`. The default is the project's public network.
- `userDataBase64`. If the machines do not run their cloud-init, set it to `true`. OVHcloud does not say whether the
  API wants the user data encoded.

**Worth knowing.**

- **Instances have no tags or metadata**. So Geass writes ownership into the machine's **name**, and reads it back
  from the name: `geass-<c>-<p>-<a>-<node>`. In this name, `c` and `p` are short hashes of the cluster and the pool. A
  name of another shape does not belong to Geass, and Geass never lists it. The name takes lowercase letters, digits,
  and hyphens, and its fixed prefix is 24 characters. So **keep pool names short**. A long node name makes a name
  longer than a 63-character host name, which OVHcloud might refuse.
- **There is no firewall in the Public Cloud API.** Machines are behind the project's default security group. This
  group lets everything in, and OVHcloud says not to edit it. The machines accept SSH only with the cluster's key, and
  the kubelet and the overlay authenticate their peers. If that is not enough, restrict the project's network
  yourself. The connection warns about this.
- A new project is limited, and cannot create instances until you add a payment method. Both show as quota errors. The
  connection tells you when the project's quota in the default region is used up. A project that is not `ok` also
  cannot create instances.
- Billing is hourly, for every hour that started. The root disk is part of the instance, and OVHcloud removes it with
  the instance.

## Scaleway

**Boundary.** One Scaleway Project that only Geass uses.

**Credentials.** An API key's **secret key**, and the Project's ID. Geass does not need the access key. Give the key an
IAM policy with `InstancesFullAccess` and `BlockStorageFullAccess` on that Project only. Scaleway refuses a key
without a policy for the Project with 403.

**Region and sizes.** A zone: `fr-par-1`, `fr-par-2`, `fr-par-3`, `nl-ams-1`, `nl-ams-2`, `nl-ams-3`, `pl-waw-1`,
`pl-waw-2`, or `pl-waw-3`. A zone is necessary, in the pool or in the connection. For the instance types, use
`geass sizes`.

**Settings.** `securityGroupId` uses your own security group instead of the group that Geass makes. `rootVolumeGb`
sets the root volume for sizes that boot from block storage. The default is the image's own size, about 10 GB.

**Worth knowing.**

- To make a machine, Geass makes three calls: create the server (which stays stopped), set its cloud-init, and power it
  on. A repeated create finishes the step that is missing. If this call made a server and could not start it, Geass
  deletes the server again before it returns the error. So a stock-out at power-on leaves nothing behind.
- When Scaleway deletes a server, it removes only the server and its local disks. Scaleway only detaches a
  block-storage root volume, and an address that is not dynamic stays. Both continue to bill. So Geass first reads
  their IDs, then terminates the server, and then deletes them through their own APIs.
- Some sizes boot from a local disk, and other sizes boot only from block storage. The same image label names both.
  Geass selects the image that the size can boot.
- An API key can have an expiry date that Geass cannot read from the secret key. Check the date in the console, and
  renew the key before then. The connection has that warning.
- The check cannot say whether a key can create servers. A key whose policy only reads passes the check.

## UpCloud

**Boundary.** A sub-account that only Geass uses. A sub-account cannot touch what the main account has.

**Credentials.** One of these:

- An API token (`ucat_...`). Make it for Geass, with the shortest life that you can renew, and a year at most.
- The user name and password of a sub-account that has API access and can create servers and storages.

When you give both, the token has priority.

**Region and sizes.** A zone: `de-fra1`, `fi-hel1`, `fi-hel2`, `nl-ams1`, `uk-lon1`, `pl-waw1`, `es-mad1`, `se-sto1`,
`us-nyc1`, `us-chi1`, `us-sjo1`, `sg-sin1`, or `au-syd1`. A zone is necessary, in the pool or in the connection. Sizes
are UpCloud plans, for example `2xCPU-4GB`, with prices from UpCloud's price list in your account's currency.

**Settings.** `diskGb` is the size of the machine's disk. The default is what the plan includes, or 25 GB for a plan
that includes no disk.

**Worth knowing.**

- Geass makes a server in one call, and the server starts by itself. Its disk is a clone of the public Ubuntu Server
  24.04 template. The server gets a public IPv4 interface and a utility (private) IPv4 interface.
- The firewall is for each server. Only a server that has left maintenance accepts rules, and the rules then take one
  to two minutes to apply. Geass writes the rules as soon as the server allows it. Geass reports the machine as
  pending until the rules are in place, so Geass never gives out the machine without them. The rules end with a drop
  of everything else.
- UpCloud can delete a server only when the server is stopped. Its storage stays and bills, unless the delete says to
  remove it. So Geass stops a started server, and then deletes the stopped server together with its storage and
  backups. This takes two passes.
- **The token expires**, within a year, and Geass does not read the date. Record the date, and renew the token before
  then. The connection warns when the account's limits restrict how far a pool can scale. These limits are fewer than
  8 CPU cores, or fewer than 5 public IPv4 addresses.
- A sub-account without the permission to create servers passes the check, and fails when Geass makes the first
  machine.

## Vultr

**Boundary.** An account user that only Geass uses. Vultr's API keys have no scopes. Their permissions are the account
user's permissions.

**Credentials.** An API key from a user who can **provision** and manage **firewalls**. To make the key, go to Account,
API, Enable API. The connection reads the user's permissions, and warns about a missing `provisioning` or `firewall`.
If the key's access control lists addresses, include the address that the operator runs from. If you do not, every
later call fails with 403.

**Region and sizes.** A region ID: `fra`, `ams`, `lhr`, `cdg`, `sto`, `waw`, `mad`, `ewr`, `atl`, `ord`, `dfw`,
`lax`, `sjc`, `sea`, `mia`, `yto`, `sgp`, `nrt`, `icn`, `syd`, or `bom`. A region is required. Sizes are Cloud Compute
plans (`vc2-2c-4gb`). A plan has a monthly price. An hour costs the monthly price divided by 672, because Vultr limits
what it bills in a month.

**Settings.** `firewallGroupId` attaches your own firewall group, and Geass changes nothing in it.

**Worth knowing.**

- The image is `Ubuntu 24.04 LTS x64`, unless the pool names another image: a name from Vultr's list, or a numeric
  ID.
- A firewall group has no rule "update". After a firewall group has an inbound rule, it drops everything that is not
  allowed. Rules are added and removed one call at a time. Geass finds its group by its description,
  `geass-<cluster>`. Geass adds the rules that are missing, and then removes the rules that are not wanted. So a
  machine always has SSH while the rules change.
- Vultr's Ubuntu images might ship with `ufw` switched on and only SSH allowed. Geass switches `ufw` off at first boot,
  because the firewall group is the filter.
- Billing is hourly, with one hour at least.

## Your own service (webhook)

Use a webhook for a provider that Geass has no driver for. For example, a hosting company with an API of its own, a
hypervisor, or a script that powers on a spare server. You run an HTTP service, and Geass calls it. The service
manages all other parts of the machine.

**Credentials.** You need these values:

- `url`: an address under which Geass calls `/machines`, for example `https://machines.example.com/geass`.
- An optional bearer token.

Geass refuses these addresses:

- An address that is not `http` or `https`.
- A link-local address, where cloud metadata services answer.
- A connection to such an address, however Geass gets to it: a name that resolves to one, or a redirect to one.

Geass does not use a proxy for the service. Geass allows loopback and private addresses, because a service inside your
network is a normal place for one. When there is a token, every request has `Authorization: Bearer <token>`. Every
body is JSON.

**The contract.**

| Request | What your service does |
| --- | --- |
| `POST /machines` | Create a machine from the request body, and answer 200, 201, or 202 with the machine. It must be idempotent on `name`. A second request with the name of a machine that exists answers with that machine, and makes no machine. |
| `GET /machines/{id}` | The machine, or 404 when it does not exist. |
| `GET /machines?cluster=<id>&pool=<name>` | `{"machines": [...]}`, only the machines made for that cluster (and pool, when given). So your service does not need to trust Geass to filter. |
| `DELETE /machines/{id}` | Delete the machine and what only it used. 404 and any 2xx both mean that it is gone. |
| `GET /sizes?region=` | Optional. `{"sizes": [{"id", "vcpus", "memoryMiB", "diskGB", "hourlyPrice", "currency", "arch"}]}`. Without it, `geass sizes` shows nothing, and Geass passes on the pool's size as it is. |
| `GET /verify` | Optional. `{"account": "...", "warnings": [...], "expiresAt": "RFC 3339"}`. |

The create request has these fields: `name`, `region`, `size`, `image`, `userData` (the cloud-init document),
`authorizedKeys`, `labels`, `spot`, `settings` (every setting of the pool, whatever its name), `controlPlaneCIDRs`,
`multiSite`, and `privateAddress`.

A machine is `{"id", "name", "state", "publicIPv4", "privateIPv4", "region", "size", "labels", "createdAt"}`. The
`state` is one of `pending`, `running`, `stopped`, `terminating`, and `gone`.

Your service must do these things:

- Store `labels` and return them, because Geass finds its machines by them.
- Apply `userData`, so that the machine starts with Geass's login.

Geass takes a `401` or `403` as refused credentials, and a `429` as "slow down". There is no way to say "no capacity"
or "over quota". Geass retries any other failure.

## What Geass makes at the cloud

For each machine:

- **A bare Ubuntu server** with a public IPv4 address, and one login. The login is the user `geass`, with passwordless
  `sudo`, and its key is the cluster's key. Geass makes that key. Geass puts only its **public** half and the machine's
  own host key in the machine's cloud-init. Geass never puts the join token there.
- **Labels**, which Geass writes when it makes the machine: `geass-managed=true`, `geass-cluster` (the cluster's
  identity), `geass-pool`, and `geass-node`. Geass lists its machines by these labels. The labels also prevent one
  Geass installation from touching the machines of another installation in the same account.
- **A firewall**, for every cloud that has an API for one, as the table earlier on this page shows. The firewall lets
  in SSH and the kubelet port from the control plane's addresses, and the overlay port in a multi-site cluster. Geass
  keeps the rules that it makes equal to what the cluster needs. Geass does not change rules that it did not make,
  and never edits a firewall that you name yourself in `settings`.
- Nothing else that bills. Geass makes no snapshots or backups. When Geass deletes a machine, the machine's disk and
  address are deleted with it. The drivers for Scaleway and UpCloud make extra calls to do this, because those clouds
  keep a disk or an address after the server goes.

The control plane's own firewall is a different thing. Only AWS and Hetzner connections can change it, and only when
you name it with [`controlPlaneFirewall`](/docs/guides/node-pools/#let-geass-open-the-control-plane).

## Prices and billing

`geass sizes` and the dashboard's size list show a price for an hour where the cloud's API gives one. There is no price
for Amazon EC2, Google Cloud, Oracle Cloud, and OVHcloud. The price is the list price of the machine, in the cloud's
currency, without tax. The price does not include these costs:

- Public IPv4 addresses.
- Traffic.
- Disks that the cloud bills on their own line.
- Anything else that the cloud adds.

A pool's estimated cost multiplies the price by the number of machines that the pool has.

Most clouds bill each started hour at least, and the drivers for Linode, OVHcloud, and Vultr say so. So a machine
removed after ten minutes costs a full hour. Oracle Cloud bills per second, with a one-minute minimum. For the other
clouds, read your provider's terms. If the cloud bills a started hour, set `minNodeLifetimeMinutes` on the pool to 55.
See [Node pools](/docs/guides/node-pools/#cost).

## How far to trust it

**Amazon EC2 has run against a live account.** On 19 October 2026, in `eu-north-1`, with an IAM user's key under the policy earlier on this page:

- The driver ran alone against EC2, with these results:
  - Verify worked, and the account name came from STS.
  - The driver listed 349 sizes.
  - A machine made two times with the same name was one machine.
  - The machine ran with a public address, and sshd answered through the security group that Geass made.
  - The driver found the machine again by its labels.
  - The driver opened and closed a rule on another group.
  - The driver deleted the machine two times without error.
- A real cluster ran the whole path. The control plane was an EC2 machine installed in
  [multi-provider mode](/docs/operations/multi-provider/). These things occurred:
  - A pool with `maxNodes: 2` made two machines when pods could not start. The pool joined the machines over SSH in
    about 50 seconds, and the scheduler placed pods on them.
  - A pod on a pool node got to a pod on the control plane, and to the cluster's DNS, across the WireGuard overlay.
  - The pods that did not fit stayed pending, with the message that the pool was at its maximum.
  - About 90 seconds after the load went, the pool removed both nodes. The machines were terminated, and the rules
    that Geass had opened on the control plane's group were removed.
- A pool with `minNodes: 1` made a machine with no load. These things also occurred:
  - Geass detected a machine terminated from outside Geass within about 40 seconds, and replaced it in about 70
    seconds.
  - A request that exceeded the account's vCPU limit showed as a node that waited with the problem `Quota` and AWS's
    own message.
  - Three refused launches in a row paused the pool. The `geass.dev/resume` annotation ended the pause.
  - When the pool was deleted, its machines were terminated.
  - A single server added from the dashboard became a node, and was removed again, with its machine terminated.
- The dashboard listed the real sizes in the pool form, made the pool, and showed it.
- Temporary credentials with a session token, from a role assumed for an hour and renewed before they expired:
  - AWS accepted them.
  - AWS refused the same credentials with a corrupted token.
  - AWS accepted the renewed credentials.
  - A pool launched and terminated machines with them.

  What AWS answers for an expired token comes from AWS's documentation.

What did **not** run, even at Amazon:

- Spot machines. The test account's organization denied the launch with a service control policy, so the spot
  request itself is not tested.
- Private addresses.
- A subnet or security group of your own.
- Fallback sizes and regions that take effect. A quota refusal waits, as designed.
- The sweep for machines that no node owns.
- Regions other than `eu-north-1`.
- Arm sizes.
- A cluster that spans two clouds.

Nothing ran for longer than a few hours. The eleven other drivers have only run against the simulation.

Each of the other drivers was written from the official API documentation of the provider. Each one is tested against
an in-memory simulation of that API, which the author also wrote. **None of them has run against a live account.** In
practice, this means:

- A provider can answer in a way that its documentation does not describe. It can also have a quota, a permission, or
  an image that the simulation does not know. The driver comments name the places where the documentation was not
  clear.
- The connection check is read-only for most clouds. The cloud cannot tell Geass, without creating something, whether
  a token can create machines. So a connection can show **Available**, and the first machine can still fail. The
  connection's warnings tell you which clouds this is true for. A pool stops after three failed machines in a row,
  which limits what that costs.
- The permissions for every cloud except Amazon EC2 come from the calls that the drivers make. They have not run
  against a live account. A call that a role refuses shows as the node's message.

To try a cloud safely, do these steps:

1. Use a project of its own.
2. Make a pool with `maxNodes: 1` and `minNodes: 1`.
3. Watch the pool with `geass pools show`.
4. Delete the pool.
5. In the cloud's own console, make sure that nothing is left.
6. Then increase the numbers.

If something behaves differently from this page, [open an issue](https://github.com/degoke/geass/issues).

## What happens under the hood

- A connection is a `GeassCloudConnection` in `geass-system`, with `spec.purposes` that contains `Compute`, and no
  `spec.project`. Its credentials are in the Secret `<name>-credentials`. See
  [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/).
- The operator builds the cloud's driver from the Secret. The operator keeps the driver until the connection or the
  Secret changes. So a cloud that trades the credentials for a token (Azure, Google Cloud, OVHcloud) does this one
  time, not on every pass.
- The connection controller calls the driver's check (`Verify`) and writes `status`. The node and pool controllers
  call `Sizes`, `Create`, `Get`, `List`, and `Delete`. Geass calls nothing else at the cloud.

```bash title="Inspect connections"
kubectl -n geass-system get geasscloudconnections
kubectl -n geass-system get geasscloudconnection hetzner -o yaml
```

## Troubleshooting

**The connection says it is not accepted.** Read its message with `geass connections show NAME`. The message is the
cloud's own. These are the usual causes:

- A mistyped or revoked key.
- An expired credential, for example Azure's client secret or UpCloud's token.
- A token without the correct scope or permission.
- A key limited to addresses that do not include the operator's address (Vultr).
- A project, compartment, or resource group that does not exist, or that the credentials cannot see.

**The connection is available and the first machine fails.** The check could not test creation. Read the node's
message, which has the cloud's own error. `geass pools show NAME` lists the message of each node. Compare the
credentials with the section for that cloud earlier on this page.

**`CredentialsMissing`.** The Secret is missing, or does not have a required field. The message names the cloud's
field labels. Replace the credential with `geass connections update NAME --credential-file KEY=PATH`.

**After I replaced the Secret by hand nothing changed.** Geass checks the connection again within 15 minutes, or within
2 minutes if the connection was failing. To ask immediately, use `geass connections verify NAME`.

**A pool says the connection "is not for compute".** Its purposes do not include `Compute`. An AWS connection made for
buckets never creates machines. Make another connection.

**A pool says the connection "belongs to project ...".** A connection for machines cannot be scoped to a project. Make
a connection that is not scoped to a project.

**The cloud says it has no capacity, or the account is at a limit.** A node waits, and its message tells you which
problem it has. The pool then tries its fallback size and region. See
[Node pools](/docs/guides/node-pools/#fallback-sizes-and-regions). For a limit, ask the cloud to raise it, or delete
machines that you no longer use.

**I cannot remove the connection.** A pool or a machine uses the connection. Geass refuses to remove it, so that those
machines can still be deleted at the cloud. Delete the pool or the machines first. `geass connections list` shows what
uses each connection.

**Machines are left at the cloud after I removed things.** See
[Machines no node owns](/docs/guides/node-pools/#machines-no-node-owns), and delete the machines in the cloud's
console. When you remove a connection, Geass does not revoke its credentials at the cloud. Revoke them at the cloud
too.

## Related

- [Node pools and autoscaling](/docs/guides/node-pools/)
- [Add servers](/docs/getting-started/add-servers/)
- [Cloud connectors](/docs/guides/connectors/)
- [Connect AWS](/docs/guides/connectors/aws/) (buckets and backups)
- [Nodes at more than one provider](/docs/operations/multi-provider/)
- [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/)
- [Security](/docs/operations/security/)
