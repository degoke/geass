---
title: "Cloud connections for machines"
description: "Let Geass create and delete machines at 11 clouds and a webhook: least-privilege credentials, regions, sizes, settings and what to check. Experimental."
sidebar:
  label: Clouds for machines
---

A connection for machines holds the credentials Geass uses to create and delete servers at a cloud. [Node pools](/docs/guides/node-pools/) and servers you add one at a time use it: Geass creates the machine, installs K3s on it over SSH, and joins it to your cluster. This page explains how such a connection works, what credentials to make for each cloud, and what is particular about each one.

Geass can create machines at Amazon EC2, Microsoft Azure, DigitalOcean, Google Cloud, Hetzner Cloud, Linode (Akamai Cloud), Oracle Cloud, OVHcloud Public Cloud, Scaleway, UpCloud and Vultr, and, for any other provider, through a webhook: an HTTP service of your own that follows a small contract.

:::caution
Creating machines is **experimental**. Amazon EC2 has been run against a live account, in one region. The other eleven drivers were written from the provider's official API documentation and tested against an in-memory simulation of that API only. See [How far to trust it](#how-far-to-trust-it) before you give a connection credentials that can spend money.
:::

## How a connection for machines works

- **It belongs to the cluster, never to a project.** A project must not be able to spend money on machines, so a connection that creates them cannot be scoped to a project. Geass refuses a pool or a server that names one that is.
- **Only an admin can use it.** Making, changing and removing a connection, listing a cloud's sizes, and making a pool or a server with it need the admin role. A viewer or observer can see the connection's name, cloud, whether it works and what uses it, never its credentials, its cloud account or what the cloud said about it. A project token cannot reach connections at all.
- **It has a purpose, `Compute`.** Every cloud on this page makes machines, so a connection to any of them has the purpose `Compute` unless you list others. AWS is the one cloud that also holds [buckets and backups](/docs/guides/connectors/aws/): an AWS connection made before purposes existed is for `Storage` and `Database` only and **never** starts creating machines. Make a separate AWS connection for machines, with its own key (see [Amazon EC2](#amazon-ec2)). The two cannot be one connection in practice: storage connections must belong to a project and machine connections must not.
- **The credentials are stored in a Secret and never shown again.** The dashboard and the command line write them into the Secret `<name>-credentials` in `geass-system`, which only the operator reads. Replacing one means typing it again.
- **Geass asks the cloud whether it accepts them.** It does at once, and then every 15 minutes, and again when you change the connection or ask with `geass connections verify`. A connection that does not work is asked again every 2 minutes. A new value that you put into the Secret yourself is noticed at the next of those checks.

### What the connection says about itself

`geass connections show NAME` and the **Server clouds** list show:

| What | Meaning |
| --- | --- |
| Status | `accepted`, `not accepted` (with the cloud's message) or `not checked yet`. The dashboard says **Available** or **Not available**. |
| Account | The account, project or tenant the credentials act on, as the cloud reports it. |
| Last accepted | When the cloud last accepted the credentials. |
| Credentials expire | When they stop working, for the clouds that say. Geass adds a warning from 14 days before. |
| Warnings | Things that work now and are likely to fail later. Each cloud's own warnings are listed below. |
| Used by | The pools and machines that depend on the connection. |

In Kubernetes these are `status.available`, `status.account`, `status.message`, `status.verifiedAt`, `status.expiresAt` and `status.warnings` of the [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/), and the `Ready` condition is `True` when the cloud accepted the credentials, `False` with `VerificationFailed` when it did not, and `False` with `CredentialsMissing` when the Secret lacks a required key.

The check reads from the cloud and changes nothing. For most clouds it cannot show that the credentials may **create** a machine without creating one, and says so in a warning, so the first machine is the real test (see [How far to trust it](#how-far-to-trust-it)). Only Amazon EC2 asks the cloud, with a dry run, whether a launch would be allowed.

## The one rule for credentials: a boundary of their own

Whatever the cloud calls it, give Geass a project, a resource group, a compartment or an account that holds nothing else, and credentials that reach only that. Geass lists and deletes the machines that carry its labels, and nothing else, but a boundary of its own is what makes that true even if something goes wrong: the credentials cannot touch what is outside it, and everything inside is Geass's to delete. It also gives you one bill to read. The sections below say what each cloud's boundary is and what the driver calls, so that you grant exactly that and no more. Where the driver's own text names a role or a scope, this page uses that name. Where it does not, it lists the calls and leaves the cloud's own action names to the cloud's documentation: do not take more than the calls need.

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

`geass providers` prints the same table from what the operator knows, and `geass providers NAME` explains each credential and setting. The same lists are in the dashboard's form.

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

"No" under spot means the driver ignores `spot: true`. A cluster name in a firewall's name is shortened where the cloud limits the length.

## Add a connection

### In the dashboard

1. Open **Settings**, then **Connectors**. Under **Server clouds** choose **Connect a cloud**.
2. Choose the **Cloud**, name the connection (lowercase letters, numbers and hyphens; pools and servers refer to it by that name), and give the **Default region** if you want one.
3. Fill in the credentials. A secret is a password box that is never filled in again. For AWS the form also offers **Buckets** and **Databases** next to the purpose Compute (shown as **Servers**) that it always has: leave them off, and make a separate connection for buckets.
4. Choose **Connect**. Geass asks the cloud whether it accepts the credentials, and the card says **Available** or shows the cloud's message. **Verify now** asks again, **Edit** changes the default region or replaces credentials (type only the ones that change), and **Remove** deletes the connection and its Secret.

### With the command line

```bash
geass providers Hetzner                     # what it takes
geass connections add hetzner --provider Hetzner --credential-file token=./hetzner-token.txt
```

The command makes the connection, asks the cloud at once, and prints what the cloud said, including its warnings. Do not put a secret on the command line, where the shell's history and the process list keep it. Give it from a file (`--credential-file KEY=PATH`, and `-` reads standard input), or in the environment as `GEASS_CREDENTIAL_` and the key in capitals, with an underscore at each capital (`GEASS_CREDENTIAL_TOKEN`, `GEASS_CREDENTIAL_ACCESS_KEY_ID`). A key of several lines (a Google Cloud service account key, an Oracle Cloud private key) has to come from a file. A required secret that is still missing is asked for, without showing it, when a person is at the keyboard.

```bash
geass connections add aws-compute --provider AWS --region eu-north-1 \
  --credential accessKeyId=AKIA... --credential-file secretAccessKey=./secret.txt
geass connections list
geass connections show hetzner
geass connections update hetzner --credential-file token=./new-token.txt   # rotate
geass connections verify hetzner
```

This command makes only connections for machines. A connection for buckets or databases is made in the dashboard. See [`geass connections`](/docs/reference/cli/#geass-connections).

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

The Secret's keys are the credential names in the table above. Do not set `spec.project`. After you change a Secret by hand, ask again with `geass connections verify hetzner`.

## Choose a region and a size

```bash
geass sizes --connection hetzner --region fsn1 --min-vcpus 2 --min-memory 4 --limit 10
```

prints the sizes the cloud offers there, cheapest first (or smallest first, where the cloud states no price), with their processors, memory, disk, architecture and the price for an hour in the cloud's currency. Geass asks the cloud with the connection's credentials, so this needs an admin. A node reserves about 250m CPU and 700 MiB of memory for K3s and the system pods, so a size under 2 vCPU and 4 GiB leaves little. The `ARCH` column says `amd64` or `arm64`: a node of one architecture can only run images built for it.

## Amazon EC2

**Boundary.** An IAM user used only by Geass, in an AWS account (or with conditions) that holds nothing you cannot lose. Make a separate connection from any you use for buckets, and keep the key apart: the key that creates buckets and IAM users must not also launch instances.

**What the driver calls.** `sts:GetCallerIdentity` (needs no permission); the reads `DescribeInstances`, `DescribeInstanceTypes`, `DescribeImages`, `DescribeSubnets` and `DescribeSecurityGroups`; `RunInstances` (and a `DryRun` of it to check the key may launch); `CreateTags` through the tags it puts on a launch; `TerminateInstances`; and, for the security group it makes, `CreateSecurityGroup`, `AuthorizeSecurityGroupIngress` and `RevokeSecurityGroupIngress`. A launch tags the instance and its volume `geass-managed=true`, `geass-cluster`, `geass-pool` and `geass-node`.

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

- `Describe` calls take no resource-level conditions in EC2, so they are open (`"Resource": "*"`). A key that may launch only instances tagged `geass-managed=true` therefore cannot list only those, and Geass narrows the list with a tag filter in each request.
- To confine the key to the regions you use, add one more statement rather than a condition on every statement. The policy above is 1,708 bytes without spaces, and an inline user policy may hold only 2,048, so conditions on all eight statements no longer fit. A managed policy may hold 6,144.

  ```json
  {
    "Sid": "OnlyInEuNorth1",
    "Effect": "Deny",
    "Action": "ec2:*",
    "Resource": "*",
    "Condition": { "StringNotEquals": { "aws:RequestedRegion": "eu-north-1" } }
  }
  ```
- Add this statement only if you use [`controlPlaneFirewall`](/docs/guides/node-pools/#let-geass-open-the-control-plane), with the control plane's own security group: Geass adds and removes rules there, so it may do it on that group (and the key may be this one, or the one of another connection at the same account). It marks each rule with the description `geass`:

  ```json
  {
    "Sid": "OpenTheControlPlanesGroup",
    "Effect": "Allow",
    "Action": ["ec2:AuthorizeSecurityGroupIngress", "ec2:RevokeSecurityGroupIngress"],
    "Resource": "arn:aws:ec2:eu-north-1:123456789012:security-group/sg-0abc123"
  }
  ```
- With `spot` the account also needs what AWS wants for Spot machines (the Spot service-linked role). The driver makes no call for it, so this policy does not cover it: if a spot launch is refused, the error names the missing action.
- This policy has been run against a live account in `eu-north-1`, as an IAM user's key with exactly these statements, the region guard above and the control plane statement: it launched, tagged and terminated machines, made and changed the cluster's security group, listed sizes and opened and closed rules on the control plane's group. A call it refuses shows up as the node's message, naming the action. An organization's service control policy can also refuse a launch whatever the key's policy says, and the message names it.

**Region and sizes.** An AWS region such as `eu-north-1`. A pool's `region` wins, then the connection's default, then `us-east-1`. Sizes are the current-generation, non-bare-metal instance types with 1 to 32 vCPUs. EC2's API states no prices, so they are listed smallest first and show no price.

**Settings.** `subnetId` launches into that subnet: it must give instances a public address, or the pool's `addressType` must be `Private`. Without it, Geass uses a default-VPC subnet. `securityGroupId` uses a group you made, as it is, instead of Geass's. `diskGb` sets the root disk (default 30, at least 8).

**Worth knowing.**

- The security group is `geass-<cluster>`, made once in the VPC of the subnet. It lets SSH and the kubelet port in from the control plane's addresses, and, in a multi-site cluster, the overlay's UDP port from anywhere. Geass keeps those three rules equal to what the cluster needs and leaves rules on other ports alone.
- Instances accept only IMDSv2 and one network hop, so pods cannot read the instance's credentials.
- Burstable instances (the `t` families) are launched with standard credits: the default, unlimited, can bill beyond the hourly price.
- The image is the newest Canonical Ubuntu 24.04 for the instance type's architecture.
- EC2 shows a machine a moment after you launch it, and lists a terminated one for about an hour. Geass waits and ignores the terminated ones.
- A root volume is deleted with the instance, and so is the address EC2 assigned.

### Temporary credentials

Instead of an IAM user's key, the connection can hold the credentials that `sts:AssumeRole` returns: an access key ID that starts with `ASIA`, a secret access key and a **session token**. Put the token in the optional secret credential `sessionToken`. Geass then sends it with every request to EC2 and STS, as the header `X-Amz-Security-Token`, and signs it with the request. Without a `sessionToken` nothing changes: requests are signed as they always were. The role needs the permissions of the policy above. Only a connection for machines reads the token: a connection for [buckets and backups](/docs/guides/connectors/aws/) uses an IAM user's key.

Temporary credentials expire (after an hour, unless the role says otherwise), so whatever issues them has to replace them before they lapse. Replace the key, the secret and the token together, in one request, and send `expiresAt` with them if you want the connection to show when they lapse:

```bash title="Replace temporary credentials"
curl -fsS -X PATCH https://geass.example.com/api/v1/connections/aws-compute \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Content-Type: application/json" \
  -d '{"accessKeyId": "ASIA...", "secretAccessKey": "...", "sessionToken": "...", "expiresAt": "2026-10-22T14:30:00Z"}'
```

`geass connections update aws-compute --credential-file KEY=PATH` does the same, one `--credential-file` for each key.

- **A new key is used at once.** The operator builds the driver for a connection from its Secret, and builds it again whenever the Secret changes, so the next machine Geass makes, looks at or deletes uses the new credentials. Nothing is restarted. The connection's own status (Available, the account, Credentials expire) is refreshed at the next check, which is within 15 minutes. Ask for it at once with `geass connections verify aws-compute` or `POST /api/v1/connections/aws-compute/verify`.
- **Credentials that lapse before new ones arrive** make AWS answer `ExpiredToken`, and the connection shows AWS's message and is not available. Geass does not take that for credentials that were refused for good, which would fail the node: a machine waits to be made or deleted, with AWS's message as the node's message, and carries on when new credentials arrive. A node that is running is not replaced meanwhile: Geass cannot ask AWS about its machine, and asks again a minute later. A machine that cannot be deleted keeps billing until new credentials arrive.
- **AWS does not say when credentials end.** `GetCallerIdentity` has no expiry, so Geass shows one only if you send `expiresAt`, an RFC 3339 time such as `2026-10-22T14:30:00Z`. It appears as Credentials expire (`status.expiresAt`). With a session token Geass adds no "expires in N days" warning, which credentials that are renewed every hour would carry all the time. An `expiresAt` that is not a time is a warning, and changes nothing else. On a key with no session token `expiresAt` works as it does for any cloud, with the warning from 14 days before.
- **A key or token that was set cannot be cleared through the API**: a field you leave out or send empty keeps its value. To go back from temporary credentials to an IAM user's key, make a new connection, or remove `sessionToken` and `expiresAt` from the Secret `<name>-credentials` with `kubectl`.

## Microsoft Azure

**Boundary.** A resource group that Geass has to itself, and a custom role scoped to it. Azure cannot scope a role by tag, so the group is the boundary.

**Credentials.** A Microsoft Entra app registration (service principal) with a client secret, then the five fields in the table. Give the service principal a custom role on the resource group with these, and no more: virtual machines, disks, network interfaces, public IP addresses and network security groups, in the group; the join action on the subnet the pool uses (the subnet is in a virtual network that Geass does not manage); and the read of VM sizes at the subscription (`Microsoft.Compute/locations/vmSizes/read`), which a role scoped to the group does not include. Look up the exact action names in Azure's documentation; the connection warns if the sizes cannot be read.

**What the driver calls.** It reads the resource group, lists the group's virtual machines, and reads the subscription's VM sizes for a location. It creates a machine with one PUT of the virtual machine, in which the network interface, the public address and the OS disk are described and made by Azure, each with its delete option set to `Delete` so that deleting the machine deletes them and nothing keeps billing. It reads the interface and the public address back, and reads and writes network security groups. It reads prices from Azure's public price list, without sending any credential there.

**Region and sizes.** An Azure location name such as `westeurope`, `northeurope`, `uksouth`, `eastus2` or `swedencentral`. Sizes come from the subscription's list for the location, with prices in US dollars from the public price list. Azure gives no architecture there, so a size whose name has a `p` in its suffix, such as `Standard_D4ps_v5`, is taken to be ARM and gets the arm64 image.

**Settings.** `subnetId` is required: the full resource ID of an existing subnet in the pool's region. `nsgId` uses your own network security group, which must let in what the cluster needs. `imageVersion` pins the version of the Ubuntu image (default `latest`). `diskGb` is the OS disk (default 30). The pool's `image` may be `publisher:offer:sku:version` or the resource ID of a gallery or custom image.

**Worth knowing.**

- **The client secret expires**, after at most 24 months, and Azure does not tell a client secret how long it has left. When it does, every call fails with `AADSTS7000222`. Note the date, and renew the secret before then (`geass connections update NAME --credential-file clientSecret=PATH`). The connection warns about this; it cannot count down.
- **Azure cannot say whether the role is complete** without doing something. A role that lacks the right to create, join the subnet or write security groups passes the connection check and fails when the first machine is made.
- Public addresses are Standard SKU and static, and accept nothing until a network security group allows it, so every machine has one: yours, or Geass's, named `geass-<cluster>-<location>`. Geass's lets in SSH and the kubelet port from the control plane (and the overlay port from anywhere in a multi-site cluster), then **denies everything else coming in, including the virtual network's default allow**. Other machines in the same virtual network cannot reach the new one except through those rules.
- A machine of the same name that belongs to another cluster is an error and is never touched.
- A machine whose provisioning fails is shown as stopped, and is deleted like any other. An allocation failure that Azure only reports afterwards therefore shows up as a machine that never runs.

## DigitalOcean

**Boundary.** A Team that Geass has to itself.

**Credentials.** A personal access token with custom scopes: `droplet` (create, read, delete), `tag` (create, read), `firewall` (create, read, update), `image:read`, `vpc:read`, `sizes:read` and `regions:read`. A full-access token also works. Add `account:read` if you want Geass to show the Droplet limit and the account's status; without it a warning says so.

**Region and sizes.** A region slug: `nyc1`, `nyc3`, `sfo3`, `tor1`, `ams3`, `lon1`, `fra1`, `sgp1`, `blr1`, `syd1`. Empty lets DigitalOcean choose. Sizes are Droplet slugs such as `s-2vcpu-4gb`, all amd64. GPU sizes are left out, because Geass would never use the GPU.

**Settings.** `firewallId` attaches each machine to your own Cloud Firewall (its UUID), and Geass changes nothing in it. `vpcUuid` creates the machine in that VPC instead of the region's default.

**Worth knowing.**

- DigitalOcean caps the Droplets an account may have, and the connection warns when the account is at its limit. Ask for the limit to be raised before you set a large `maxNodes`. The limit and a token that lacks a scope both arrive as an error that looks alike, and Geass tells them apart by the message.
- Droplets carry tags, not labels, so Geass's labels are written as tags such as `geass-pool:burst`. Geass makes one Cloud Firewall per cluster, `geass-<cluster>`, aimed at a tag so that a machine is covered the moment it exists, with every outbound connection allowed. A firewall with no outbound rule would block all of it.
- A Droplet's name is not unique, and DigitalOcean has no idempotency token, so Geass looks for the node's Droplet by tag and name before it makes another.
- Deleting a Droplet deletes only the Droplet. Geass makes no snapshots, backups, volumes or reserved addresses, so nothing else bills.
- The API allows 250 requests a minute, and Geass waits when it is told to slow down.
- The check cannot see a token's scopes: a token without `droplet:create`, `tag:create` or `firewall:create` passes it and fails when the first machine is made.

## Google Cloud

**Boundary.** A project that Geass has to itself.

**Credentials.** A service account JSON key from that project (IAM, Service accounts, Keys, Add key, JSON; paste the whole file, or give it with `--credential-file serviceAccountKey=PATH`). Give the service account `roles/compute.instanceAdmin.v1` and `roles/compute.securityAdmin`. The first cannot make firewall rules.

**Region and sizes.** Here a region is a **zone**: `europe-west4-a`, `us-central1-b`, `asia-southeast1-a`. Machine types and stock differ per zone. Compute Engine's API states no prices, so sizes are listed smallest first.

**Settings.** `network` and `subnetwork` choose where to put the machine (a name or a full path; a subnetwork is required when the network uses custom subnets). `image` is a source image or image family (default Ubuntu 24.04 LTS for the machine type's architecture). `diskSizeGb` (default 30) and `diskType`. `firewallTag` names a network tag your own firewall rules already target, and Geass then makes no rules. `skipFirewall` set to `true` makes none and adds no tag: you must open tcp 22 and 10250 and udp 51820 yourself.

**Worth knowing.**

- **Organisations created since May 2024 refuse to create service account keys** (the `iam.disableServiceAccountKeyCreation` policy). An administrator may have to lift it for the project before the key can exist.
- **`roles/compute.instanceAdmin.v1` does not grant `compute.firewalls.create` or `update`.** Without `roles/compute.securityAdmin`, or the `skipFirewall` or `firewallTag` setting, creating a machine fails with an error that names the permission, rather than making a machine nothing can reach. Google cannot say whether the key may make firewall rules without making one, so the connection always carries that warning.
- Geass makes two global firewall rules, `geass-<cluster>` (SSH and the kubelet) and `geass-<cluster>-overlay` (the overlay's UDP port, switched off unless the cluster is multi-site), aimed at the network tag `geass-<cluster>` that it puts on the machine. Rules carry no labels, so they are recognised by their description, which starts with "Managed by Geass".
- No service account is attached to the machine, so it cannot reach other Google APIs. The boot disk and the ephemeral external address are deleted with the machine. OS Login is switched off for the machine, so a project that turns it on does not stop the login Geass makes through cloud-init.
- Compute Engine reports a quota or a stock-out only inside the long-running operation of the insert, so Geass watches that operation for a few seconds and returns the failure.

## Hetzner Cloud

**Boundary.** A Hetzner Cloud Project that Geass has to itself. A token belongs to one Project, so nothing outside it can be touched.

**Credentials.** An API token with **Read & Write** permission (Project, Security, API tokens).

**Region and sizes.** A location: `fsn1`, `nbg1`, `hel1` (Europe), `ash`, `hil` (US), `sin` (Singapore). Empty lets Hetzner choose. Sizes are server types such as `cx23`, with the price per location.

**Settings.** `firewallId` uses your own firewall instead of Geass's. `networkId` also attaches the machine to that private network.

**Worth knowing.**

- A read-only token passes the connection check and fails when the first machine is made: Hetzner cannot tell the two apart without creating something. The connection warns about it.
- Geass makes a firewall `geass-<cluster>` once and keeps its rules right: SSH and the kubelet port from the control plane (SSH from anywhere if it knows no control plane address), and the overlay's UDP port from anywhere in a multi-site cluster. Everything else coming in is dropped.
- The machine gets a public IPv4 address and no IPv6. Deleting a server releases its address.
- Hetzner is one of the two clouds [`controlPlaneFirewall`](/docs/guides/node-pools/#let-geass-open-the-control-plane) works for: Geass adds one rule per protocol and port, with the description `geass-access`, to the firewall you name, and takes the machine's address out again when it goes.

## Linode (Akamai Cloud)

**Boundary.** An account that Geass has to itself.

**Credentials.** A personal access token with read/write on Linodes, Firewalls and IPs (Profile, API Tokens).

**Region and sizes.** A region id that supports Metadata, such as `us-east`, `us-ord`, `us-iad`, `us-lax`, `eu-west`, `eu-central`, `ap-south`, `ap-northeast` or `ap-southeast`. A region is required, in the pool or in the connection. Sizes are Linode types such as `g6-standard-2`; the list leaves out the ones a region does not sell, and GPU types.

**Settings.** `firewallId` attaches your own Cloud Firewall (its number), and Geass changes nothing in it.

**Worth knowing.**

- The machine's cloud-init document goes in the Linode's Metadata user data, which only some regions support. Geass reads the region's capabilities and fails with a plain message when it lacks `Metadata`, rather than making a machine that never learns its key.
- Geass asks for legacy configuration interfaces, which it needs to attach a firewall at creation. An account whose `interfaces_for_new_linodes` setting is `linode_only` refuses that, and the create fails with a message that says what to change. Geass does not read that setting, so the connection always carries a warning about it.
- Creating from an image needs a root password. Geass sends a random one that it never stores or shows: the login is the key in cloud-init, which turns password login off.
- Labels travel as tags, and a long label is split over several tags. The Linode's label is cleaned up to Linode's rules.
- Billing is hourly, rounded up. Deleting a Linode releases its addresses and deletes its disks.
- The check cannot see a token's scopes: a token that cannot write Linodes or Firewalls passes it.

## Oracle Cloud

**Boundary.** A compartment that Geass has to itself.

**Credentials.** An API signing key: a user's RSA key **without a passphrase**, and that user's tenancy OCID, user OCID, the key's fingerprint (`aa:bb:cc:...`), and the compartment's OCID (`ocid1.compartment...`, or `ocid1.tenancy...` for the root). The user must be allowed to manage instance-family and use virtual-network-family in that compartment, and to list the tenancy's availability domains (unless you give the `availabilityDomain` setting). The clock of the machine the operator runs on must be within five minutes of Oracle's: a skewed clock makes every request a 401.

**Region and sizes.** A region identifier: `eu-frankfurt-1`, `us-ashburn-1`, `uk-london-1`, `ap-sydney-1`. Only the commercial realm is supported. A size is the shape's name for a fixed shape (`VM.Standard2.2`), and for a Flex shape `SHAPE:OCPUS:MEMORY_GB`, such as `VM.Standard.E4.Flex:2:8`. An OCPU is two vCPUs on x86 and one on Arm, and `geass sizes` reports vCPUs. Oracle's API states no prices.

**Settings.** `subnetId` is required: the subnet to attach the machine to, which must allow a public address. `availabilityDomain` (a name such as `Uocm:EU-FRANKFURT-1-AD-1`; by default Geass tries the tenancy's first, then the others when one has no capacity), `imageId`, `nsgId` (network security groups, separated by commas) and `diskSizeGb` (at least 50).

**Worth knowing.**

- **Geass does not touch the network.** The subnet's security list, or the network security group in `nsgId`, must let in tcp 22 and 10250 from the control plane, and udp 51820 in a multi-site cluster, or machines are made and never join. The connection says so in a warning.
- "Out of host capacity" is common in some regions, and is a stock-out in one availability domain. A pool with fallbacks moves on after three minutes; see [Fallback sizes and regions](/docs/guides/node-pools/#fallback-sizes-and-regions).
- Oracle's Ubuntu images carry an operating system firewall that lets in SSH and ends with a reject. The first-boot script opens udp 51820 and tcp 10250 with `iptables`, ahead of the reject, and saves the rules.
- Oracle bills per second, with a one-minute minimum. Oracle keeps a terminated instance in its list for about twelve hours: Geass sees it as gone.
- Launching is idempotent through a retry token, which Oracle honours for 24 hours.

## OVHcloud Public Cloud

**Boundary.** A Public Cloud project that Geass has to itself. This is the **Public Cloud** product, the one that can be created and deleted by the hour through the API. The VPS product cannot be autoscaled and is not what Geass talks to.

**Credentials.** An OAuth2 service account (`clientId` and `clientSecret`, made with `POST /me/api/oauth2/client`, flow `CLIENT_CREDENTIALS`; the secret is shown once), the project's ID (`serviceName`, 32 hexadecimal characters), and the `endpoint` where your OVHcloud account lives: `eu` (the default), `ca` or `us`. The service account has no rights until an IAM policy gives it some on the project: it must be able to list flavors, images and networks, and to create, read and delete instances.

**Region and sizes.** A Public Cloud region the project has enabled: `GRA11`, `SBG5`, `RBX-A` (Europe), `BHS5` (Canada), `WAW1`, `DE1`, `UK1`, `SGP1`, `SYD1`. **Enable the region in the project first**: Geass never enables one, because that is your decision. A size is the flavor's name, such as `d2-4`, `b3-8` or `c3-16`; each region has its own flavor ids, which Geass looks up from the name. The calls Geass makes return no prices.

**Settings.** `imageId` (image ids differ per region; the default is the region's Ubuntu 24.04), `networkId` (default: the project's public network), and `userDataBase64`: set it to `true` if the machines do not run their cloud-init, because OVHcloud does not say whether the API wants the user data encoded.

**Worth knowing.**

- **Instances have no tags or metadata**, so ownership is written into the machine's **name** and read back from it: `geass-<c>-<p>-<a>-<node>`, where `c` and `p` are short hashes of the cluster and the pool. A name of another shape is not Geass's and is never listed. The name takes lowercase letters, digits and hyphens, and its fixed prefix is 24 characters, so **keep pool names short**: a long node name makes a name longer than a 63-character host name, which OVHcloud may refuse.
- **There is no firewall in the Public Cloud API.** Machines sit behind the project's default security group, which lets everything in and which OVHcloud says not to edit. They accept SSH only with the cluster's key, and the kubelet and the overlay authenticate their peers, but restrict the project's network yourself if that is not enough. The connection warns about it.
- A new project is limited and cannot create instances until a payment method is added. Both show up as quota errors, and the connection says when the project's quota in the default region is used up. A project that is not `ok` cannot create instances either.
- Billing is hourly, every commenced hour. The root disk is part of the instance and is removed with it.

## Scaleway

**Boundary.** One Scaleway Project that Geass has to itself.

**Credentials.** An API key's **secret key** (the access key is not needed), and the Project's ID. Give the key an IAM policy with `InstancesFullAccess` and `BlockStorageFullAccess` on that Project only. A key without a policy for the Project is refused with 403.

**Region and sizes.** A zone: `fr-par-1`, `fr-par-2`, `fr-par-3`, `nl-ams-1`, `nl-ams-2`, `nl-ams-3`, `pl-waw-1`, `pl-waw-2`, `pl-waw-3`. A zone is needed, in the pool or in the connection. Use `geass sizes` for the instance types.

**Settings.** `securityGroupId` uses your own security group instead of Geass's. `rootVolumeGb` sets the root volume for sizes that boot from block storage (default: the image's own, about 10 GB).

**Worth knowing.**

- Making a machine is three calls: create the server (which stays stopped), set its cloud-init, power it on. A repeated create finishes whichever is missing. A server this call made and could not start is deleted again before the error is returned, so a stock-out at power-on leaves nothing behind.
- Deleting a server removes it and its local disks only. A block-storage root volume is merely detached, and an address that is not dynamic stays, and both keep billing, so Geass reads their ids first, terminates the server and then deletes them through their own APIs.
- Some sizes boot from a local disk and others only from block storage, and the same image label names both. Geass picks the one the size can boot.
- An API key can have an expiry date that Geass cannot read from the secret key: check it in the console and renew the key before then. The connection carries that warning.
- The check cannot say whether a key may create servers: a key whose policy only reads passes it.

## UpCloud

**Boundary.** A sub-account that Geass has to itself. A sub-account cannot touch what the main account has.

**Credentials.** Either an API token (`ucat_...`, made for Geass with the shortest life you can renew, at most a year) or the user name and password of a sub-account that has API access and may create servers and storages. A token wins when both are given.

**Region and sizes.** A zone: `de-fra1`, `fi-hel1`, `fi-hel2`, `nl-ams1`, `uk-lon1`, `pl-waw1`, `es-mad1`, `se-sto1`, `us-nyc1`, `us-chi1`, `us-sjo1`, `sg-sin1`, `au-syd1`. A zone is needed, in the pool or in the connection. Sizes are UpCloud plans such as `2xCPU-4GB`, with prices from UpCloud's price list in your account's currency.

**Settings.** `diskGb` is the size of the machine's disk. Default: what the plan includes, or 25 GB for a plan that includes none.

**Worth knowing.**

- A server is made in one call and starts by itself. Its disk is a clone of the public Ubuntu Server 24.04 template, and it gets a public and a utility (private) IPv4 interface.
- The firewall is per server, and only a server that has left maintenance accepts rules, which then take one to two minutes to apply. Geass writes them as soon as the server lets it and reports the machine as pending until they are in place, so that it is never handed out without them. They end with a drop of everything else.
- A server can be deleted only when it is stopped, and its storage stays and bills unless the delete says to remove it. Geass therefore stops a started server and deletes the stopped one together with its storage and backups, which takes two passes.
- **The token expires**, within a year, and Geass does not read the date. Note it and renew the token before then. The connection warns when the account's limits (fewer than 8 CPU cores, or fewer than 5 public IPv4 addresses) cap how far a pool can scale.
- A sub-account without the permission to create servers passes the check and fails when the first machine is made.

## Vultr

**Boundary.** An account user that Geass has to itself. Vultr's API keys have no scopes: their permissions are the account user's.

**Credentials.** An API key (Account, API, Enable API) from a user who may **provision** and manage **firewalls**. The connection reads the user's permissions and warns about a missing `provisioning` or `firewall`. If the key's access control lists addresses, include the one the operator runs from, or every later call fails with 403.

**Region and sizes.** A region id: `fra`, `ams`, `lhr`, `cdg`, `sto`, `waw`, `mad`, `ewr`, `atl`, `ord`, `dfw`, `lax`, `sjc`, `sea`, `mia`, `yto`, `sgp`, `nrt`, `icn`, `syd`, `bom`. A region is required. Sizes are Cloud Compute plans (`vc2-2c-4gb`); a plan carries a monthly price, and an hour costs the monthly price divided by 672, because Vultr caps what it bills in a month.

**Settings.** `firewallGroupId` attaches your own firewall group, and Geass changes nothing in it.

**Worth knowing.**

- The image is `Ubuntu 24.04 LTS x64` unless the pool names another (a name from Vultr's list, or a numeric id).
- A firewall group has no rule "update": once it has an inbound rule, everything not allowed is dropped, and rules are added and removed one call at a time. Geass finds its group by its description, `geass-<cluster>`, adds the rules that are missing and then removes the ones that are not wanted, so a machine is never without SSH while they change.
- Vultr's Ubuntu images may ship with `ufw` switched on and only SSH allowed. Geass switches it off at first boot, because the firewall group is the filter.
- Billing is hourly, with at least one hour.

## Your own service (webhook)

For a provider Geass has no driver for: a hosting company with an API of its own, a hypervisor, a script that powers on a spare server. You run an HTTP service, and Geass calls it. Everything else about the machine is the service's business.

**Credentials.** `url`, an address that Geass calls `/machines` under (`https://machines.example.com/geass`), and an optional bearer token. An address that is not `http` or `https`, or that is a link-local address (where cloud metadata services answer), is refused. So is a connection to such an address however it is reached: a name that resolves to one, or a redirect to one. Geass does not use a proxy for the service. Loopback and private addresses are allowed, because a service inside your network is a normal place for one. Every request carries `Authorization: Bearer <token>` when there is one, and every body is JSON.

**The contract.**

| Request | What your service does |
| --- | --- |
| `POST /machines` | Create a machine from the request body and answer 200, 201 or 202 with the machine. It must be idempotent on `name`: a second request with the name of a machine that exists answers with that machine and makes none. |
| `GET /machines/{id}` | The machine, or 404 when it does not exist. |
| `GET /machines?cluster=<id>&pool=<name>` | `{"machines": [...]}`, only the machines made for that cluster (and pool, when given), so that your service need not trust Geass to filter. |
| `DELETE /machines/{id}` | Delete the machine and what only it used. 404 and any 2xx both mean it is gone. |
| `GET /sizes?region=` | Optional. `{"sizes": [{"id", "vcpus", "memoryMiB", "diskGB", "hourlyPrice", "currency", "arch"}]}`. Without it, `geass sizes` shows nothing and the pool's size is passed on as it is. |
| `GET /verify` | Optional. `{"account": "...", "warnings": [...], "expiresAt": "RFC 3339"}`. |

The create request carries `name`, `region`, `size`, `image`, `userData` (the cloud-init document), `authorizedKeys`, `labels`, `spot`, `settings` (every setting of the pool, whatever its name), `controlPlaneCIDRs`, `multiSite` and `privateAddress`. A machine is `{"id", "name", "state", "publicIPv4", "privateIPv4", "region", "size", "labels", "createdAt"}`, with `state` one of `pending`, `running`, `stopped`, `terminating` and `gone`. Your service must store `labels` and return them, because Geass finds its machines by them, and apply `userData` so the machine starts with Geass's login. A `401` or `403` is taken as refused credentials, a `429` as "slow down", and there is no way to say "no capacity" or "over quota": any other failure is retried.

## What Geass makes at the cloud

For each machine:

- **A bare Ubuntu server** with a public IPv4 address, and one login: the user `geass`, with passwordless `sudo`, whose key is the cluster's. Geass makes that key, and puts only its **public** half and the machine's own host key in the machine's cloud-init. The join token is never put there.
- **Labels**, written when the machine is made: `geass-managed=true`, `geass-cluster` (the cluster's identity), `geass-pool` and `geass-node`. They are what Geass lists its machines by, and what keeps one Geass installation from touching the machines of another in the same account.
- **A firewall**, for every cloud that has an API for one, as the table above says: SSH and the kubelet port from the control plane's addresses, and the overlay port in a multi-site cluster. Geass keeps the rules it makes equal to what the cluster needs, leaves rules it did not make alone, and never edits a firewall you name yourself in `settings`.
- Nothing else that bills: Geass makes no snapshots or backups, and deleting a machine deletes its disk and its address with it. (The drivers for Scaleway and UpCloud make extra calls to do that, because those clouds keep a disk or an address after the server goes.)

The control plane's own firewall is a different thing: only AWS and Hetzner connections can change it, and only when you name it with [`controlPlaneFirewall`](/docs/guides/node-pools/#let-geass-open-the-control-plane).

## Prices and billing

`geass sizes` and the dashboard's size list show a price for an hour where the cloud's API gives one: not for Amazon EC2, Google Cloud, Oracle Cloud and OVHcloud. The price is the list price of the machine, in the cloud's currency, without tax. It does not include public IPv4 addresses, traffic, disks the cloud bills on their own line, or anything else the cloud adds. A pool's estimated cost multiplies it by the machines the pool has.

Most clouds bill each started hour at least (the drivers for Linode, OVHcloud and Vultr say so), so a machine removed after ten minutes costs a full hour. Oracle Cloud bills per second with a one-minute minimum. For the others, read your provider's terms. Where a started hour is billed, set `minNodeLifetimeMinutes` on the pool to 55. See [Node pools](/docs/guides/node-pools/#cost).

## How far to trust it

**Amazon EC2 has run against a live account.** On 19 October 2026, in `eu-north-1`, with an IAM user's key under the policy above:

- The driver ran alone against EC2: verify (the account is named from STS), 349 sizes listed, a machine made twice with the same name is one machine, running with a public address and sshd answering through the security group Geass made, found again by its labels, a rule opened and closed on another group, and deleted twice without error.
- A real cluster ran the whole path. The control plane was an EC2 machine installed in [multi-provider mode](/docs/operations/multi-provider/). A pool with `maxNodes: 2` made two machines when pods could not start, joined them over SSH in about 50 seconds, and the scheduler placed pods on them. A pod on a pool node reached a pod on the control plane and the cluster's DNS across the WireGuard overlay. The pods that did not fit stayed pending with the message that the pool was at its maximum. About 90 seconds after the load went the pool removed both nodes, the machines were terminated and the rules Geass had opened on the control plane's group were removed.
- A pool with `minNodes: 1` made a machine with no load. A machine terminated from outside Geass was noticed within about 40 seconds and replaced in about 70. A request that exceeded the account's vCPU limit showed as a node waiting with the problem `Quota` and AWS's own message. Three refused launches in a row paused the pool, and the `geass.dev/resume` annotation ended the pause. Deleting a pool terminated its machines. A single server added from the dashboard became a node and was removed again, with its machine terminated.
- The dashboard listed the real sizes in the pool form, made the pool, and showed it.
- Temporary credentials with a session token, from a role assumed for an hour and renewed before they lapsed: AWS accepted them, refused the same credentials with a corrupted token, and accepted the renewed ones, and a pool launched and terminated machines with them. What AWS answers for a lapsed token is taken from its documentation.

What did **not** run, even at Amazon: spot machines (the test account's organization denied the launch with a service control policy, so the spot request itself is untested), private addresses, a subnet or security group of your own, fallback sizes and regions taking effect (a quota refusal waits, as designed), the sweep for machines no node owns, regions other than `eu-north-1`, Arm sizes, and a cluster that spans two clouds. Nothing ran for longer than a few hours. The eleven other drivers have only run against the simulation.

For the other drivers, every one was written from the provider's official API documentation, and every one is tested against an in-memory simulation of that API that the author also wrote. **None of them has run against a live account.** What that means in practice:

- A provider can answer in a way its documentation does not describe, or have a quota, a permission or an image that the simulation does not know. The driver comments name the places where the documentation was unclear.
- The connection check is read-only for most clouds. The cloud cannot tell Geass, without creating something, whether a token may create machines. So a connection can show **Available** and the first machine can still fail. The connection's warnings say which clouds this holds for. A pool stops after three failed machines in a row, which bounds what that costs.
- The permissions for every cloud but Amazon EC2 come from the calls the drivers make. They have not been run against a live account, and a call that a role refuses shows as the node's message.

To try a cloud safely: use a project of its own, make a pool with `maxNodes: 1` and `minNodes: 1`, watch it with `geass pools show`, delete it, and check the cloud's own console that nothing is left. Then turn the numbers up. If something behaves differently from this page, please [open an issue](https://github.com/degoke/geass/issues).

## What happens under the hood

- A connection is a `GeassCloudConnection` in `geass-system` with `spec.purposes` containing `Compute` and no `spec.project`, and its credentials are in the Secret `<name>-credentials`. See [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/).
- The operator builds the cloud's driver from the Secret and keeps it until the connection or the Secret changes, so a cloud that trades the credentials for a token (Azure, Google Cloud, OVHcloud) does it once and not on every pass.
- The connection controller calls the driver's check (`Verify`) and writes `status`. The node and pool controllers call `Sizes`, `Create`, `Get`, `List` and `Delete`. Nothing else at the cloud is called.

```bash title="Inspect connections"
kubectl -n geass-system get geasscloudconnections
kubectl -n geass-system get geasscloudconnection hetzner -o yaml
```

## Troubleshooting

**The connection says it is not accepted.** Read its message with `geass connections show NAME`. It is the cloud's own. Usual causes: a mistyped or revoked key, an expired credential (Azure's client secret, UpCloud's token), a token without the right scope or permission, a key limited to addresses that do not include the operator's (Vultr), or a project, compartment or resource group that does not exist or that the credentials cannot see.

**The connection is available and the first machine fails.** The check could not test creation. Read the node's message (`geass pools show NAME` lists each node's), which carries the cloud's own error, and compare the credentials with the section for that cloud above.

**`CredentialsMissing`.** The Secret is missing or lacks a required field. The message names the cloud's field labels. Replace the credential: `geass connections update NAME --credential-file KEY=PATH`.

**After I replaced the Secret by hand nothing changed.** The connection is checked again within 15 minutes, or within 2 if it was failing. Ask at once with `geass connections verify NAME`.

**A pool says the connection "is not for compute".** Its purposes do not include `Compute`. An AWS connection made for buckets never creates machines: make another.

**A pool says the connection "belongs to project ...".** A connection for machines cannot be scoped to a project. Make one that is not.

**The cloud says it has no capacity, or the account is at a limit.** A node waits and its message says which. The pool then tries its fallback size and region. See [Node pools](/docs/guides/node-pools/#fallback-sizes-and-regions). For a limit, ask the cloud to raise it, or delete machines you no longer use.

**I cannot remove the connection.** A pool or a machine uses it, and Geass refuses so that those machines can still be deleted at the cloud. Delete them first. `geass connections list` shows what uses each connection.

**Machines are left at the cloud after I removed things.** See [Machines no node owns](/docs/guides/node-pools/#machines-no-node-owns), and delete them in the cloud's console. Removing a connection does not revoke its credentials at the cloud: revoke them there too.

## Related

- [Node pools and autoscaling](/docs/guides/node-pools/)
- [Add servers](/docs/getting-started/add-servers/)
- [Cloud connectors](/docs/guides/connectors/)
- [Connect AWS](/docs/guides/connectors/aws/) (buckets and backups)
- [Nodes at more than one provider](/docs/operations/multi-provider/)
- [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/)
- [Security](/docs/operations/security/)
