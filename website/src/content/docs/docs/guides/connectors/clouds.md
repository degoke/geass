---
title: "Cloud connections for machines"
description: "Let Geass create and delete machines at 11 clouds and a webhook: least-privilege credentials, regions, sizes, settings, and checks. Experimental."
sidebar:
  label: Clouds for machines
---

A connection for machines holds the credentials that Geass uses to create and delete servers at a cloud.
[Pools](/docs/guides/node-pools/) use it, and so do servers that you add one at a time. Geass creates the machine,
sets it up over SSH, and joins it to your cluster. This page tells you which credentials to make for each cloud, and
what is special about each cloud.

Geass can create machines at Amazon EC2, Microsoft Azure, DigitalOcean, Google Cloud, Hetzner Cloud, Linode (Akamai
Cloud), Oracle Cloud, OVHcloud Public Cloud, Scaleway, UpCloud, and Vultr. For any other provider, Geass can call a
webhook: an HTTP service of your own.

:::caution
Read [How far to trust it](#how-far-to-trust-it) before you give a connection credentials that can spend money.
Machine creation is **experimental**. Amazon EC2 has run against a live account, in one region. The other eleven
drivers were tested only against a simulation of the API of each provider.
:::

## How a connection for machines works

- **It belongs to the cluster, never to a project.** A project must not spend money on machines. Geass refuses a pool
  or a server that names a connection of a project.
- **Only an admin can use it.** Only an admin can make, change, and remove a connection, list the sizes of a cloud,
  and make a pool or a server. A viewer can see the name of the connection, its cloud, whether it works, and what uses
  it. A viewer never sees its credentials or its cloud account.
- **It is for servers.** An AWS connection for machines can also be for buckets and databases, but keep them separate.
  An AWS connection for buckets must belong to a project, and a connection for machines must not. See
  [Amazon EC2](#amazon-ec2).
- **Geass never shows the credentials again.** To replace a credential, enter it again.
- **Geass asks the cloud whether it accepts the credentials.** Geass asks immediately, then every 15 minutes, and
  every 2 minutes while the connection does not work. Geass also asks when you change the connection, or click
  **Verify now**.

### What the connection says about itself

The **Server clouds** list and `geass connections show NAME` show these items:

| Item | Meaning |
| --- | --- |
| Status | **Available**, **Not available** with the message of the cloud, or **Checking** before the first check. |
| Account | The account, project, or tenant that the credentials act on, as the cloud reports it. |
| Last accepted | When the cloud last accepted the credentials. |
| Credentials expire | When the credentials stop working, for the clouds that give this date. Geass warns from 14 days before. |
| Warnings | Things that work now and are likely to fail later. The section of each cloud lists its own warnings. |
| Used by | The pools and machines that use the connection. |

The check only reads from the cloud. For most clouds, the check cannot show that the credentials can **create** a
machine. The connection tells you this in a warning. So the first machine is the real test. Only Amazon EC2 can check,
with a dry run, that a launch is allowed.

## Give Geass a boundary of its own

Give Geass a project, a resource group, a compartment, or an account that holds nothing else. Give Geass credentials
that reach only that boundary. Geass lists and deletes only the machines that have its labels. A boundary of its own
also gives you these advantages:

- The credentials cannot touch anything outside the boundary.
- Everything inside the boundary is for Geass to delete.
- You get one bill to read.

The section of each cloud names the boundary and what the driver calls. Grant exactly that, and no more.

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
| [OVHcloud](#ovhcloud-public-cloud) | `OVHcloud` | `clientId`, `clientSecret` (secret), `serviceName`, `endpoint` (optional) | A Public Cloud region that the project has enabled: `GRA11` | `imageId`, `networkId`, `userDataBase64` |
| [Scaleway](#scaleway) | `Scaleway` | `secretKey` (secret), `projectId` | A zone: `fr-par-1` | `securityGroupId`, `rootVolumeGb` |
| [UpCloud](#upcloud) | `UpCloud` | `token` (secret), or `username` and `password` (secret) | A zone: `de-fra1` | `diskGb` |
| [Vultr](#vultr) | `Vultr` | `apiKey` (secret) | A region ID: `fra`. Required | `firewallGroupId` |
| [Your own service](#your-own-service-webhook) | `Webhook` | `url`, `token` (secret, optional) | What your service takes | Any |

The form in the dashboard has the same lists. `geass providers NAME` explains each credential and setting.

| Cloud | Spot machines | Price in the size list | Firewall that Geass makes |
| --- | --- | --- | --- |
| Amazon EC2 | Yes | No | A security group `geass-<cluster>` |
| Microsoft Azure | Yes | Yes, from the public price list of Azure (USD) | A network security group `geass-<cluster>-<location>` |
| DigitalOcean | No | Yes | A Cloud Firewall `geass-<cluster>` |
| Google Cloud | Yes | No | Two global firewall rules |
| Hetzner Cloud | No | Yes | A firewall `geass-<cluster>` |
| Linode | No | Yes | A Cloud Firewall |
| Oracle Cloud | Yes | No | None: you open the ports |
| OVHcloud | No | No | None: the cloud has no firewall API |
| Scaleway | No | Yes | A security group |
| UpCloud | No | Yes | A firewall on each server |
| Vultr | No | Yes | A firewall group |
| Webhook | Passed on | What the service says | What the service does |

"No" under spot means that the driver ignores a spot request.

## Add a connection

### In the dashboard

1. Open **Settings** → **Connectors**.
2. Under **Server clouds**, click **Connect a cloud**.
3. Select the **Cloud**.
4. Enter a **Name**. Use lowercase letters, numbers, and hyphens. Pools and servers use the connection by that name.
5. Optional: enter a **Default region**. Geass uses it when a pool or a server names no region.
6. Enter the credentials. The form never shows a secret again after you save it.
7. For AWS, leave **Buckets** and **Databases** under **Also use for** off. Make a separate connection for buckets.
8. Click **Connect**. Geass asks the cloud whether it accepts the credentials. The card shows **Available**, or the
   message of the cloud.

Each connection card has these buttons:

- **Verify now** asks the cloud again.
- **Edit** changes the default region or replaces credentials. Enter only the credentials that change.
- **Remove** deletes the connection and its credentials.

### From the command line

```bash
geass providers Hetzner                     # what the cloud takes
geass connections add hetzner --provider Hetzner --credential-file token=./hetzner-token.txt
geass connections list
geass connections show hetzner
geass connections update hetzner --credential-file token=./new-token.txt   # rotate
geass connections verify hetzner
```

`geass connections add` makes the connection, asks the cloud immediately, and prints the answer and its warnings.
This command makes only connections for machines. See [`geass connections`](/docs/reference/cli/#geass-connections).

Do not put a secret on the command line. The shell history and the process list keep it. Give a secret in one of
these ways:

- From a file, with `--credential-file KEY=PATH`. The value `-` reads standard input.
- In the environment, as `GEASS_CREDENTIAL_` followed by the key in capitals, with an underscore before each capital
  in the key. For example, `GEASS_CREDENTIAL_ACCESS_KEY_ID`.

A key of several lines must come from a file. Examples are a Google Cloud service account key and an Oracle Cloud
private key. If a required secret is missing and a person is at the keyboard, the command asks for it.

## Choose a region and a size

In the dashboard, the pool form lists the sizes of the selected region in **Machine size**. From the command line,
run this command:

```bash
geass sizes --connection hetzner --region fsn1 --min-vcpus 2 --min-memory 4 --limit 10
```

The list shows the cheapest size first. When the cloud gives no price, the smallest size is first. Each size shows its
processors, memory, disk, architecture, and price for an hour in the currency of the cloud.

Each server keeps about 250m CPU and 700 MiB of memory for the system. A size under 2 vCPU and 4 GiB leaves little
for your services. A server of one architecture (`amd64` or `arm64`) runs only images built for that
architecture.

## Amazon EC2

**Boundary.** An IAM user that only Geass uses. Put the user in an AWS account that holds nothing that you cannot
lose, or limit the user with conditions. Keep this key separate from the key that you use for buckets. The key that
creates buckets and IAM users must not also launch instances.

**What the driver calls.** The driver makes these calls:

- `sts:GetCallerIdentity`, which needs no permission.
- `DescribeInstances`, `DescribeInstanceTypes`, `DescribeImages`, `DescribeSubnets`, and `DescribeSecurityGroups`.
- `RunInstances`, and a dry run of it to check that the key can launch.
- `CreateTags`, through the tags that it puts on a launch.
- `TerminateInstances`.
- `CreateSecurityGroup`, `AuthorizeSecurityGroupIngress`, and `RevokeSecurityGroupIngress`, for the security group
  that it makes.

A launch tags the instance and its volume with `geass-managed=true`, `geass-cluster`, `geass-pool`, and `geass-node`.

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

- `Describe` calls in EC2 take no resource-level conditions, so they use `"Resource": "*"`. Geass filters each list
  by tag.
- To limit the key to the regions that you use, add one more statement. Do not add a condition to every statement.
  An inline user policy can hold only 2,048 bytes, and the policy is already 1,708 bytes without spaces.

  ```json
  {
    "Sid": "OnlyInEuNorth1",
    "Effect": "Deny",
    "Action": "ec2:*",
    "Resource": "*",
    "Condition": { "StringNotEquals": { "aws:RequestedRegion": "eu-north-1" } }
  }
  ```

- If Geass opens the [control plane firewall](/docs/guides/node-pools/#let-geass-open-the-control-plane) on the
  security group of the control plane, add this statement. The key can be this key, or the key of another connection
  to the same account. Geass marks each rule that it adds with the description `geass`.

  ```json
  {
    "Sid": "OpenTheControlPlanesGroup",
    "Effect": "Allow",
    "Action": ["ec2:AuthorizeSecurityGroupIngress", "ec2:RevokeSecurityGroupIngress"],
    "Resource": "arn:aws:ec2:eu-north-1:123456789012:security-group/sg-0abc123"
  }
  ```

- For spot machines, the account also needs the Spot service-linked role that AWS requires. This policy does not
  cover it. If AWS refuses a spot launch, the error names the missing action.
- This policy, with the region guard and the control plane statement, ran against a live account in `eu-north-1`.
  A call that the policy refuses shows as the message of the machine, and names the action. A service control policy
  of an AWS organization can also refuse a launch. The message then names that policy.

**Region and sizes.** An AWS region, for example `eu-north-1`. Geass uses the region of the pool, then the default of
the connection, then `us-east-1`. Sizes are current-generation instance types with 1 to 32 vCPUs, not bare metal. EC2
gives no prices, so the list shows the smallest size first.

**Settings.** These are the settings:

- `subnetId` launches into that subnet. The subnet must give instances a public address, or the pool must use private
  addresses. Without `subnetId`, Geass uses a subnet of the default VPC.
- `securityGroupId` uses a group that you made, as it is, instead of the group that Geass makes.
- `diskGb` sets the root disk. The default is 30, and the minimum is 8.

**Worth knowing.**

- Geass makes the security group `geass-<cluster>` one time, in the VPC of the subnet. The group lets in tcp 22 and
  10250 from the control plane. In a multi-site cluster, it also lets in udp 51820 from anywhere. Geass does not change
  rules on other ports.
- Instances accept only IMDSv2 with one network hop. So your services cannot read the credentials of the instance.
- Geass launches burstable instances (the `t` families) with standard credits. The AWS default, unlimited, can bill
  more than the hourly price.
- The image is the newest Canonical Ubuntu 24.04 for the architecture of the instance type.
- EC2 deletes the root volume and the public address with the instance.

### Temporary credentials

Instead of the key of an IAM user, the connection can hold the credentials that `sts:AssumeRole` returns:

- An access key ID that starts with `ASIA`.
- A secret access key.
- A **session token**. Put it in the optional secret `sessionToken`.

The role needs the permissions of the policy earlier on this page. Only a connection for machines takes a session
token. A connection for [buckets and backups](/docs/guides/connectors/aws/) takes only the key of an IAM user.

Temporary credentials expire, by default after an hour. The system that issues them must replace them before then.
Replace the key, the secret, and the token together, in one request. To show when the credentials expire, also send
`expiresAt`, an RFC 3339 time:

```bash title="Replace temporary credentials"
curl -fsS -X PATCH https://geass.example.com/api/v1/connections/aws-compute \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Content-Type: application/json" \
  -d '{"accessKeyId": "ASIA...", "secretAccessKey": "...", "sessionToken": "...", "expiresAt": "2026-10-22T14:30:00Z"}'
```

`geass connections update aws-compute --credential-file KEY=PATH` does the same. Give one `--credential-file` for each
key.

- **Geass uses new credentials immediately.** The next machine that Geass makes, reads, or deletes uses them, and
  nothing restarts. The status of the connection refreshes at the next check, within 15 minutes. To refresh it now,
  click **Verify now**.
- **Credentials that expire before new ones arrive** make AWS answer `ExpiredToken`. The connection is then not
  available. A machine waits to be made or deleted, and continues when new credentials arrive. Geass does not replace
  a running server in the meantime. A machine that Geass cannot delete continues to bill.
- **AWS does not say when credentials end.** Geass shows an expiry only if you send `expiresAt`. With a session token,
  Geass gives no "expires in N days" warning, because hourly credentials would show it all the time.
- **You cannot clear a key or a token through the API.** A field that you leave out or send empty keeps its value. To
  go back to the key of an IAM user, make a new connection.

## Microsoft Azure

**Boundary.** A resource group that only Geass uses, and a custom role scoped to that group. Azure cannot scope a role
by tag, so the group is the boundary.

**Credentials.** A Microsoft Entra app registration (service principal) with a client secret, and the five fields in
the table. Give the service principal a custom role on the resource group with these permissions, and no more:

- Virtual machines, disks, network interfaces, public IP addresses, and network security groups, in the group.
- The join action on the subnet that the pool uses. The subnet is in a virtual network that Geass does not manage.
- The read of VM sizes at the subscription (`Microsoft.Compute/locations/vmSizes/read`). A role scoped to the group
  does not include this permission.

Find the exact action names in the documentation of Azure. The connection warns if it cannot read the sizes.

**What the driver calls.** The driver reads the resource group, its virtual machines, and the VM sizes of a location.
It creates a machine with one request that describes the network interface, the public address, and the OS disk.
Each has its delete option set to `Delete`, so Azure deletes them with the machine. The driver also reads and writes
network security groups. It reads prices from the public price list of Azure, and sends no credential there.

**Region and sizes.** An Azure location name, for example `westeurope`, `northeurope`, `uksouth`, `eastus2`, or
`swedencentral`. Prices are in US dollars. Geass treats a size whose name has a `p` in its suffix as Arm, for example
`Standard_D4ps_v5`.

**Settings.** These are the settings:

- `subnetId` is required. It is the full resource ID of an existing subnet in the region of the pool.
- `nsgId` uses your own network security group. That group must let in what the cluster needs.
- `imageVersion` pins the version of the Ubuntu image. The default is `latest`.
- `diskGb` is the OS disk. The default is 30.

The `image` of a pool can be `publisher:offer:sku:version`, or the resource ID of a gallery image or a custom image.

**Worth knowing.**

- **The client secret expires**, after 24 months or less. Azure does not tell Geass when. After it expires, every call
  fails with `AADSTS7000222`. Record the date, and renew the secret before then.
- **Azure cannot say whether the role is complete** without creating something. A role without the right to create,
  join the subnet, or write security groups passes the check. It fails when Geass makes the first machine.
- Every machine has a network security group: yours, or `geass-<cluster>-<location>`. The group of Geass lets in tcp
  22 and 10250 from the control plane, and udp 51820 in a multi-site cluster. Then it **denies all other incoming
  traffic, including the default allow of the virtual network**.
- A machine with the same name that belongs to another cluster is an error. Geass never touches that machine.
- Azure can report an allocation failure late. Such a machine shows as stopped and never runs. Geass deletes it like
  any other machine.

## DigitalOcean

**Boundary.** A team that only Geass uses.

**Credentials.** A personal access token with these custom scopes:

- `droplet` (create, read, delete).
- `tag` (create, read).
- `firewall` (create, read, update).
- `image:read`, `vpc:read`, `sizes:read`, and `regions:read`.

A full-access token also works. To let Geass show the Droplet limit and the status of the account, add `account:read`.

**Region and sizes.** A region slug: `nyc1`, `nyc3`, `sfo3`, `tor1`, `ams3`, `lon1`, `fra1`, `sgp1`, `blr1`, or
`syd1`. Empty lets DigitalOcean choose. Sizes are Droplet slugs, for example `s-2vcpu-4gb`, all `amd64`. Geass leaves
out GPU sizes.

**Settings.** `firewallId` attaches each machine to your own Cloud Firewall, by its UUID. Geass changes nothing in that
firewall. `vpcUuid` creates the machine in that VPC instead of the default VPC of the region.

**Worth knowing.**

- DigitalOcean limits the number of Droplets in an account. The connection warns when the account is at its limit.
  Before you set a large maximum on a pool, ask DigitalOcean to raise the limit.
- Geass makes one Cloud Firewall for each cluster, `geass-<cluster>`, aimed at a tag. So the firewall covers a machine
  from its first moment. The firewall allows all outgoing traffic.
- When Geass deletes a Droplet, nothing else is left to bill.
- The check cannot see the scopes of a token. A token without `droplet:create`, `tag:create`, or `firewall:create`
  passes the check, and fails at the first machine.

## Google Cloud

**Boundary.** A project that only Geass uses.

**Credentials.** A JSON key of a service account from that project. To get the key, go to **IAM** → **Service
accounts** → **Keys** → **Add key** → **JSON**. Paste the whole file. Give the service account
`roles/compute.instanceAdmin.v1` and `roles/compute.securityAdmin`. The first role cannot make firewall rules.

**Region and sizes.** For Google Cloud, a region is a **zone**: `europe-west4-a`, `us-central1-b`, or
`asia-southeast1-a`. Machine types and stock are different in each zone. The API gives no prices, so the list shows
the smallest size first.

**Settings.** These are the settings:

- `network` and `subnetwork` select where to put the machine, as a name or a full path. A subnetwork is required when
  the network uses custom subnets.
- `image` is a source image or an image family. The default is Ubuntu 24.04 LTS for the architecture of the machine
  type.
- `diskSizeGb` (default 30) and `diskType`.
- `firewallTag` names a network tag that your own firewall rules already target. Geass then makes no rules.
- `skipFirewall` set to `true` makes no rules and adds no tag. Then open tcp 22 and 10250, and udp 51820, yourself.

**Worth knowing.**

- **Organizations created since May 2024 block service account keys** with the
  `iam.disableServiceAccountKeyCreation` policy. An administrator might have to remove this policy for the project.
- **`roles/compute.instanceAdmin.v1` cannot create or update firewall rules.** Without `roles/compute.securityAdmin`,
  `skipFirewall`, or `firewallTag`, machine creation fails, and the error names the permission. The check cannot test
  this, so the connection always has that warning.
- Geass makes two global firewall rules, aimed at the network tag `geass-<cluster>` on the machine. One rule lets in
  SSH and tcp 10250. The other rule, `geass-<cluster>-overlay`, lets in udp 51820, and is off unless the cluster is
  multi-site. Geass recognizes its rules by a description that starts with "Managed by Geass".
- The machine has no service account, so it cannot reach other Google APIs. Google Cloud deletes the boot disk and the
  public address with the machine.

## Hetzner Cloud

**Boundary.** A Hetzner Cloud project that only Geass uses. A token belongs to one project, so it cannot touch
anything outside that project.

**Credentials.** An API token with **Read & Write** permission. To make the token, go to **Project** → **Security** →
**API tokens**.

**Region and sizes.** A location: `fsn1`, `nbg1`, `hel1` (Europe), `ash`, `hil` (US), or `sin` (Singapore). Empty lets
Hetzner choose. Sizes are server types, for example `cx23`, with the price for each location.

**Settings.** `firewallId` uses your own firewall instead of the firewall that Geass makes. `networkId` also attaches
the machine to that private network.

**Worth knowing.**

- A read-only token passes the check, and fails at the first machine. The connection warns about this.
- Geass makes the firewall `geass-<cluster>` one time. It lets in tcp 22 and 10250 from the control plane, or SSH
  from anywhere when Geass knows no control plane address. In a multi-site cluster, it also lets in udp 51820 from
  anywhere. The firewall drops all other incoming traffic.
- The machine gets a public IPv4 address and no IPv6 address. Hetzner releases the address when Geass deletes the
  server.
- Hetzner is one of the two clouds where Geass can open the
  [control plane firewall](/docs/guides/node-pools/#let-geass-open-the-control-plane). Geass adds rules with the
  description `geass-access` to the firewall that you name, and removes them when the machine goes.

## Linode (Akamai Cloud)

**Boundary.** An account that only Geass uses.

**Credentials.** A personal access token with read and write access to Linodes, Firewalls, and IPs. To make the token,
go to **Profile** → **API Tokens**.

**Region and sizes.** A region ID that supports Metadata, for example `us-east`, `us-ord`, `us-iad`, `us-lax`,
`eu-west`, `eu-central`, `ap-south`, `ap-northeast`, or `ap-southeast`. A region is required, in the pool or in the
connection. Sizes are Linode types, for example `g6-standard-2`. The list leaves out GPU types and types that a region
does not sell.

**Settings.** `firewallId` attaches your own Cloud Firewall, by its number. Geass changes nothing in that firewall.

**Worth knowing.**

- Geass sets up the machine through Linode Metadata, which only some regions support. In a region without Metadata,
  Geass fails with a clear message, and makes no machine.
- Geass asks for legacy configuration interfaces, so that it can attach a firewall when it creates the machine. An
  account whose `interfaces_for_new_linodes` setting is `linode_only` refuses this. The message tells you what to
  change. The connection always has a warning about this setting.
- Linode needs a root password to create from an image. Geass sends a random password that it never stores or shows.
  The machine turns password login off.
- Billing is hourly, rounded up. Linode releases the addresses and deletes the disks of a deleted Linode.
- The check cannot see the scopes of a token. A token that cannot write Linodes or Firewalls passes the check.

## Oracle Cloud

**Boundary.** A compartment that only Geass uses.

**Credentials.** An API signing key. You need these values:

- The RSA key of a user, **without a passphrase**.
- The tenancy OCID and the user OCID of that user.
- The fingerprint of the key (`aa:bb:cc:...`).
- The OCID of the compartment (`ocid1.compartment...`, or `ocid1.tenancy...` for the root).

The user must have these permissions:

- Manage instance-family in that compartment.
- Use virtual-network-family in that compartment.
- List the availability domains of the tenancy. If you set `availabilityDomain`, the user does not need this
  permission.

The clock of the cluster must be within five minutes of the clock of Oracle. A wrong clock makes every request fail
with 401.

**Region and sizes.** A region identifier, for example `eu-frankfurt-1`, `us-ashburn-1`, `uk-london-1`, or
`ap-sydney-1`. Geass supports only the commercial realm. For a fixed shape, a size is the shape name
(`VM.Standard2.2`). For a Flex shape, a size is `SHAPE:OCPUS:MEMORY_GB`, for example `VM.Standard.E4.Flex:2:8`. An OCPU
is two vCPUs on x86 and one vCPU on Arm. The Oracle API gives no prices.

**Settings.** These are the settings:

- `subnetId` is required. The subnet must allow a public address.
- `availabilityDomain` is a name, for example `Uocm:EU-FRANKFURT-1-AD-1`. By default, Geass tries the first
  availability domain. When that domain has no capacity, Geass tries the others.
- `imageId`.
- `nsgId`: network security groups, separated by commas.
- `diskSizeGb`: at least 50.

**Worth knowing.**

- **Geass does not change the network.** The security list of the subnet, or the group in `nsgId`, must let in tcp 22
  and 10250 from the control plane. In a multi-site cluster, it must also let in udp 51820. If not, the machines never
  join. The connection warns about this.
- "Out of host capacity" is common in some regions. A pool with fallbacks moves on after three minutes. See
  [Fallback sizes and regions](/docs/guides/node-pools/#fallback-sizes-and-regions).
- Oracle bills per second, with a one-minute minimum.

## OVHcloud Public Cloud

**Boundary.** A Public Cloud project that only Geass uses. Geass uses the **Public Cloud** product, not the VPS
product.

**Credentials.** You need these values:

- An OAuth2 service account: `clientId` and `clientSecret`. Make it with `POST /me/api/oauth2/client`, flow
  `CLIENT_CREDENTIALS`. OVHcloud shows the secret one time only.
- The ID of the project (`serviceName`, 32 hexadecimal characters).
- The `endpoint` of your OVHcloud account: `eu` (the default), `ca`, or `us`.

The service account has no rights until an IAM policy gives it rights on the project. It must be able to list flavors,
images, and networks, and to create, read, and delete instances.

**Region and sizes.** A Public Cloud region that the project has enabled, for example `GRA11`, `SBG5`, `RBX-A`,
`BHS5`, `WAW1`, `DE1`, `UK1`, `SGP1`, or `SYD1`. **Turn on the region in the project first.** Geass never turns on a
region. A size is the flavor name, for example `d2-4`, `b3-8`, or `c3-16`. The list gives no prices.

**Settings.** These are the settings:

- `imageId`. The default is Ubuntu 24.04 of the region.
- `networkId`. The default is the public network of the project.
- `userDataBase64`. If the machines do not set themselves up, set it to `true`.

**Worth knowing.**

- **Instances have no tags.** Geass writes ownership into the name of the machine: `geass-<c>-<p>-<a>-<node>`. Geass
  never lists a machine whose name has another shape. The fixed part of the name is 24 characters, so
  **keep pool names short**. OVHcloud might refuse a name longer than 63 characters.
- **The Public Cloud API has no firewall.** Machines are behind the default security group of the project, which lets
  everything in. The machines accept SSH only with the key of the cluster. If that is not enough, restrict the network
  of the project yourself. The connection warns about this.
- A new project cannot create instances until you add a payment method. This shows as a quota error.
- Billing is hourly, for every hour that started.

## Scaleway

**Boundary.** One Scaleway project that only Geass uses.

**Credentials.** The **secret key** of an API key, and the ID of the project. Geass does not need the access key. Give
the key an IAM policy with `InstancesFullAccess` and `BlockStorageFullAccess` on that project only. Scaleway refuses a
key without a policy for the project with 403.

**Region and sizes.** A zone: `fr-par-1`, `fr-par-2`, `fr-par-3`, `nl-ams-1`, `nl-ams-2`, `nl-ams-3`, `pl-waw-1`,
`pl-waw-2`, or `pl-waw-3`. A zone is required, in the pool or in the connection.

**Settings.** `securityGroupId` uses your own security group instead of the group that Geass makes. `rootVolumeGb`
sets the root volume for sizes that boot from block storage. The default is the size of the image, about 10 GB.

**Worth knowing.**

- If Geass makes a server and cannot start it, Geass deletes the server again. So a stock-out leaves nothing behind.
- Scaleway keeps a block-storage root volume and a reserved address after a server is deleted. Both continue to bill.
  So Geass deletes them too.
- An API key can have an expiry date that Geass cannot read. Check the date in the console, and renew the key before
  then. The connection has that warning.
- The check cannot say whether a key can create servers. A read-only key passes the check.

## UpCloud

**Boundary.** A sub-account that only Geass uses. A sub-account cannot touch what the main account has.

**Credentials.** Give one of these:

- An API token (`ucat_...`). Make it for Geass, with the shortest life that you can renew, and a year at most.
- The user name and password of a sub-account that has API access and can create servers and storage.

When you give both, Geass uses the token.

**Region and sizes.** A zone: `de-fra1`, `fi-hel1`, `fi-hel2`, `nl-ams1`, `uk-lon1`, `pl-waw1`, `es-mad1`, `se-sto1`,
`us-nyc1`, `us-chi1`, `us-sjo1`, `sg-sin1`, or `au-syd1`. A zone is required, in the pool or in the connection. Sizes
are UpCloud plans, for example `2xCPU-4GB`, with prices in the currency of your account.

**Settings.** `diskGb` is the size of the disk. The default is what the plan includes, or 25 GB for a plan without a
disk.

**Worth knowing.**

- The firewall is on each server. Rules take one to two minutes to apply. Geass shows the machine as pending until the
  rules are in place. The rules end with a drop of all other traffic.
- UpCloud deletes only a stopped server. So Geass stops the server, and then deletes it with its storage and backups.
- **The token expires** within a year, and Geass cannot read the date. Record the date, and renew the token before
  then.
- The connection warns when the limits of the account restrict a pool: fewer than 8 CPU cores, or fewer than 5 public
  IPv4 addresses.
- A sub-account without the permission to create servers passes the check, and fails at the first machine.

## Vultr

**Boundary.** An account user that only Geass uses. Vultr API keys have no scopes. A key has the permissions of its
user.

**Credentials.** An API key from a user who can **provision** and manage **firewalls**. To make the key, go to
**Account** → **API** → **Enable API**. The connection warns about a missing `provisioning` or `firewall` permission.
If the key allows only some addresses, include the outgoing address of the server that runs Geass. If you do not,
every call fails with 403.

**Region and sizes.** A region ID, for example `fra`, `ams`, `lhr`, `cdg`, `sto`, `waw`, `mad`, `ewr`, `atl`, `ord`,
`dfw`, `lax`, `sjc`, `sea`, `mia`, `yto`, `sgp`, `nrt`, `icn`, `syd`, or `bom`. A region is required. Sizes are Cloud
Compute plans, for example `vc2-2c-4gb`. An hour costs the monthly price divided by 672.

**Settings.** `firewallGroupId` attaches your own firewall group. Geass changes nothing in that group.

**Worth knowing.**

- The image is `Ubuntu 24.04 LTS x64`, unless the pool names another image by name or by numeric ID.
- Geass finds its firewall group by the description `geass-<cluster>`. When rules change, Geass adds new rules before
  it removes old ones. So a machine always keeps SSH.
- Billing is hourly, with one hour at least.

## Your own service (webhook)

Use a webhook for a provider that Geass has no driver for. For example, a hosting company with its own API, a
hypervisor, or a script that powers on a spare server. You run an HTTP service, and Geass calls it.

**Credentials.** You need these values:

- `url`: the address under which Geass calls `/machines`, for example `https://machines.example.com/geass`.
- An optional bearer token. Geass sends it as `Authorization: Bearer <token>` with every request.

Geass refuses an address that is not `http` or `https`, and any link-local address, also through DNS or a redirect.
Loopback and private addresses are allowed. Geass does not use a proxy for the service. Every body is JSON.

**The contract.**

| Request | What your service does |
| --- | --- |
| `POST /machines` | Create a machine from the request body, and answer 200, 201, or 202 with the machine. It must be idempotent on `name`: a second request with the same name returns the machine that exists. |
| `GET /machines/{id}` | Return the machine, or 404 when it does not exist. |
| `GET /machines?cluster=<id>&pool=<name>` | Return `{"machines": [...]}`, only the machines made for that cluster, and that pool when given. |
| `DELETE /machines/{id}` | Delete the machine and what only it used. 404 and any 2xx both mean that it is gone. |
| `GET /sizes?region=` | Optional. Return `{"sizes": [{"id", "vcpus", "memoryMiB", "diskGB", "hourlyPrice", "currency", "arch"}]}`. Without it, the size list is empty, and Geass passes on the size of the pool as it is. |
| `GET /verify` | Optional. Return `{"account": "...", "warnings": [...], "expiresAt": "RFC 3339"}`. |

The create request has these fields: `name`, `region`, `size`, `image`, `userData` (the cloud-init document),
`authorizedKeys`, `labels`, `spot`, `settings` (every setting of the pool), `controlPlaneCIDRs`, `multiSite`, and
`privateAddress`.

A machine is `{"id", "name", "state", "publicIPv4", "privateIPv4", "region", "size", "labels", "createdAt"}`. The
`state` is one of `pending`, `running`, `stopped`, `terminating`, and `gone`.

Your service must store `labels` and return them, because Geass finds its machines by them. Your service must also
apply `userData`, so that the machine starts with the login of Geass.

Geass treats `401` and `403` as refused credentials, and `429` as a request to slow down. Geass retries any other
failure. There is no answer for "no capacity" or "over quota".

## What Geass makes at the cloud

For each machine, Geass makes these things:

- **A bare Ubuntu server** with a public IPv4 address and one login. The login is the user `geass`, with passwordless
  `sudo` and the key of the cluster. The machine gets only the public half of that key. Geass never puts the join
  token in the setup of the machine.
- **Labels**: `geass-managed=true`, `geass-cluster`, `geass-pool`, and `geass-node`. Geass finds its machines by these
  labels. The labels also stop one Geass installation from touching the machines of another installation in the same
  account.
- **A firewall**, for each cloud that has an API for one. The firewall lets in tcp 22 and 10250 from the control
  plane, and udp 51820 in a multi-site cluster. Geass never edits a firewall that you name in the settings of a pool.
- **Nothing else that bills.** Geass makes no snapshots or backups. The disk and the address of a machine are deleted
  with it.

The firewall of the control plane is different. Only AWS and Hetzner connections can change it, and only when you
name it in a pool. See [Let Geass open the control plane](/docs/guides/node-pools/#let-geass-open-the-control-plane).

## Prices and billing

The size list shows a price for an hour where the API of the cloud gives one. Amazon EC2, Google Cloud, Oracle Cloud,
and OVHcloud give no price. The price is the list price of the machine, in the currency of the cloud, without tax.
The price does not include public IPv4 addresses, traffic, disks billed on their own line, or other charges.

The estimated cost of a pool multiplies the price by the number of machines in the pool.

Most clouds bill each started hour. Linode, OVHcloud, and Vultr do this. So a machine removed after ten minutes costs
a full hour. Oracle Cloud bills per second, with a one-minute minimum. For the other clouds, read the terms of your
provider. If the cloud bills each started hour, set **Keep every node at least** to 55 minutes on the pool. See
[Node pools](/docs/guides/node-pools/#cost).

## How far to trust it

**Amazon EC2 has run against a live account,** on 19 October 2026, in `eu-north-1`, with the key of an IAM user under
the policy on this page. These things worked:

- The connection check, the size list, and repeated create and delete calls.
- A real cluster: a pool made two machines under load, joined them in about 50 seconds, and ran services on them.
  About 90 seconds after the load went, the pool removed both machines, and the firewall rules that Geass had opened.
- A pool with a minimum of one machine. Geass replaced a machine that was terminated outside Geass in about 70
  seconds.
- A request over the vCPU limit of the account. The machine waited with the problem `Quota` and the message of AWS.
- Three refused launches in a row paused the pool.
- A single server added from the dashboard, and removed again.
- The real sizes in the pool form of the dashboard.
- Temporary credentials with a session token, renewed before they expired.

These things did **not** run, even at Amazon:

- Spot machines. A service control policy of the test organization denied the launch.
- Private addresses, and a subnet or security group of your own.
- Fallback sizes and regions.
- The removal of machines that no server owns.
- Regions other than `eu-north-1`, and Arm sizes.
- A cluster across two clouds.
- A run longer than a few hours.

**None of the other eleven drivers has run against a live account.** Each was written from the official API
documentation of the provider, and tested against a simulation of that API. In practice, this means:

- A provider can answer in a way that its documentation does not describe. It can also have a quota, a permission, or
  an image that the simulation does not know.
- For most clouds, a connection can show **Available**, and the first machine can still fail. A pool stops after
  three failed machines in a row, which limits the cost.
- The permissions on this page for every cloud except Amazon EC2 come from the calls of the driver. A call that a role
  refuses shows as the message of the machine.

To try a cloud safely, do these steps:

1. Use a cloud project or account of its own.
2. Make a pool with a minimum and a maximum of 1 machine.
3. Watch the pool on the **Pools** page, or with `geass pools show`.
4. Delete the pool.
5. In the console of the cloud, make sure that nothing is left.
6. Then increase the numbers.

If something behaves differently from this page, [open an issue](https://github.com/degoke/geass/issues).

## Troubleshooting

**The connection is not available.** Read its message on the card, or with `geass connections show NAME`. The message
comes from the cloud. These are the usual causes:

- A mistyped or revoked key.
- An expired credential, for example the client secret of Azure or the token of UpCloud.
- A token without the correct scope or permission.
- A key limited to addresses that do not include the outgoing address of Geass (Vultr).
- A project, compartment, or resource group that does not exist, or that the credentials cannot see.

**The connection is available, and the first machine fails.** The check could not test creation. Read the message of
the machine on the pool page, or with `geass pools show NAME`. The message has the error of the cloud. Compare the
credentials with the section of that cloud on this page.

**The connection says a credential is missing.** The message names the missing field. Click **Edit**, and enter the
credential again.

**A pool says that the connection "is not for compute".** The connection is not for servers. An AWS connection made
for buckets never creates machines. Make a connection under **Server clouds**.

**A pool says that the connection "belongs to project ...".** A connection for machines cannot belong to a project.
Make a connection under **Server clouds**.

**The cloud has no capacity, or the account is at a limit.** The machine waits, and its message names the problem.
The pool then tries its fallback size and region. See
[Node pools](/docs/guides/node-pools/#fallback-sizes-and-regions). For a limit, ask the cloud to raise it, or delete
machines that you do not use.

**You cannot remove the connection.** A pool or a machine uses it. Geass refuses, so that those machines can still be
deleted at the cloud. Delete the pool or the machines first.

**Machines are left at the cloud.** See [Machines no node owns](/docs/guides/node-pools/#machines-no-node-owns), and
delete the machines in the console of the cloud. When you remove a connection, Geass does not revoke its credentials
at the cloud. Revoke them at the cloud too.

## Under the hood

- A connection for machines is a `GeassCloudConnection` in `geass-system`, with `Compute` in `spec.purposes` and no
  `spec.project`. Its credentials are in the Secret `<name>-credentials`, with the keys from
  [the table](#the-clouds). See [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/).
- The `Ready` condition is `True` when the cloud accepts the credentials. It is `False` with `VerificationFailed` or
  `CredentialsMissing` otherwise.
- After you change a Secret by hand, run `geass connections verify NAME` to check it now.
- To go from temporary credentials back to the key of an IAM user, you can also remove `sessionToken` and `expiresAt`
  from the Secret.

```bash title="Inspect connections"
kubectl -n geass-system get geasscloudconnections
kubectl -n geass-system get geasscloudconnection hetzner -o yaml
```

## Related

- [Node pools and autoscaling](/docs/guides/node-pools/)
- [Add servers](/docs/getting-started/add-servers/)
- [Cloud connectors](/docs/guides/connectors/)
- [Connect AWS](/docs/guides/connectors/aws/) (buckets and backups)
- [Nodes at more than one provider](/docs/operations/multi-provider/)
- [Security](/docs/operations/security/)
