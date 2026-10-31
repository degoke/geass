---
title: "Outbound limits"
description: "Stop organizations from sending mail directly to the internet, limit the ports that their services reach, and learn what the limits cannot stop."
---

On a cluster that several [organizations](/docs/guides/organizations-and-workspaces/) share, every organization reaches the internet from the addresses of the platform. A service can send spam directly to mail servers, or connect to a mining pool. Then other parties block those addresses for everyone. **Outbound limits** close ports to addresses outside the cluster for the projects of organizations:

- The ports that mail uses, by default.
- Optionally, more ports, or all ports except a list.

The limits are off until a platform administrator sets them. Without organizations or limits, nothing changes.

## What is limited, and how firmly

| Limit | Firm? |
| --- | --- |
| **Mail sent directly out**, on TCP 25, 465, 587, 2465, 2525, and 2587 | Yes, if the cluster enforces [network isolation](/docs/guides/network-isolation/#check-that-the-cluster-enforces-policies). |
| **More closed ports** (`blockedPorts`) | The same. |
| **Only listed ports open** (`allowedPorts`) | The same. |
| **Addresses opened on closed ports** (`allow`), for example a mail relay | The same. Only addresses, not host names. |
| **Mining** (`miningPools`) | No. It closes the default ports of mining pools. A miner on another port, on port 443, or through a proxy gets through. See [Mining](#mining). |
| **How much mail an organization sends** | Not by these limits. The [mail relay](/docs/guides/mail-relay/) limits the mail that goes through it. |

The limits apply to every environment of every project of the organization, pull request environments included. They also apply to the builds of the organization, because a build runs the Dockerfile of the organization. Geass never limits a project of no organization, or Geass itself.

## Before you start

- Set up organizations, and give projects to them.
- Geass has no dashboard or command-line setting for the limits yet. See [Under the hood](#under-the-hood). The administrators of an organization cannot change the limits.
- The cluster must enforce network isolation, with port ranges. K3s does this by default. The limits were tested only with the network plugin of K3s. Before you rely on the limits with another plugin, try them. See [Check the limits](#check-the-limits).
- Nothing restarts when you change the limits. New connections obey the limits immediately. A connection that is already open might continue until it closes.

## Set the limits

The platform sets limits for the projects of all organizations. When the platform sets limits, mail is closed. All other settings are optional. For the commands, see [Under the hood](#set-the-limits-on-the-server).

### Limits for one organization

One organization can have different limits. They take the same fields as the platform. These rules apply:

- A field that the organization sets replaces the whole value of the platform for that field.
- A field that the organization does not set comes from the platform.
- An empty `blockedPorts` or `allow` replaces the platform value with nothing.
- `allowedPorts` cannot be empty. To free an organization from the platform list, give it every port: `[{"port":1,"endPort":65535},{"port":1,"endPort":65535,"protocol":"UDP"}]`.
- The limits of an organization apply even when the platform sets none.

### The fields

| Field | Meaning |
| --- | --- |
| `mail` | `Blocked`, the default, closes TCP 25, 465, 587, 2465, 2525, and 2587. `Open` leaves them open. Other ports that some hosts use, for example 26 or 588, are not in the list. Add them to `blockedPorts`. |
| `miningPools` | `Blocked` closes TCP 3333 to 3336, 4444, 5555, 7777, 14433, 14444, and 45700. `Open`, or no value, leaves them open. See [Mining](#mining). |
| `allowedPorts` | When set, only these ports are open to addresses outside the cluster. Add UDP ports too, for example 443 for HTTP/3, or no UDP leaves the cluster. A port that is also closed stays closed. |
| `blockedPorts` | More ports to close. |
| `allow[].cidr` | An IPv4 range, written from its first address, for example `203.0.113.25/32`. Not in `169.254.0.0/16` or `127.0.0.0/8`. |
| `allow[].ports` | The ports that open to that range. At least one. |
| `allow[].description` | What the addresses are. |
| `port`, `endPort`, `protocol` | `port` is 1 to 65535. `endPort` makes a range up to it. `protocol` is `TCP`, the default, or `UDP`. |

Kubernetes refuses a port outside 1 to 65535, a range that ends before it starts, a protocol other than TCP or UDP, an empty `allowedPorts`, and an `allow` without ports. Geass refuses limits that leave no port open, and an `allow` that is not a valid range. When Geass refuses the limits, every project keeps what it had. See [The condition](#the-condition).

## Mail

When mail is closed, the services of an organization cannot reach a mail server outside the cluster. To let them send mail, give them one of these paths:

- **The Geass mail relay.** Use this path. Each organization gets its own login and its own limits on messages, recipients, size, and sender domains. Services reach the relay inside the cluster, so it needs no `allow`. See [Mail relay](/docs/guides/mail-relay/).
- **Your own relay in the cluster.** Services reach it the same way. It sends all the mail it gets, unless it does its own login and counting.
- **An `allow`** to a relay or provider outside the cluster, by its addresses. This does not work for a service whose addresses change.
- **`mail: Open` on one organization** that you trust.

## Ports

Without `allowedPorts`, every port is open except the closed ports. With `allowedPorts`, only the listed ports are open, minus the closed ports. For example, list these ports:

- 443 for registries, Git hosts, and S3 backups.
- The ports of databases outside the cluster.
- The port of a registry that does not use 443.

Two things stay open, whatever the limits say:

- **Everything inside the cluster, on every port.** DNS, other services, and the databases of the cluster continue to work.
- **The Kubernetes API port, 6443, from project environments.** Postgres databases of a project use it. Builds do not, so in builds the limits apply to this port too.

After any port is closed, ping and other protocols that are not TCP or UDP are also closed to the outside.

## Mining

These limits detect no mining. They see only addresses and ports, not programs. [Abuse detection](/docs/guides/abuse-detection/) looks for mining.

`miningPools: Blocked` closes the default ports of public mining pools. This stops a miner that uses the example configuration of a pool. It does not stop a miner on port 443, on another port, or through a proxy. So treat it as a tripwire, not as a control.

| Ports | Notes |
| --- | --- |
| 3333 to 3336, 4444, 5555, 7777 | The common pool ports. `miningPools: Blocked` closes them. |
| 14433, 14444, 45700 | More pool ports from published lists. `miningPools: Blocked` closes them. |
| 6666, 9999, 13333, 15555, 17777, 19999 | More pool ports that other software also uses. For example, IRC uses 6666. To close them, add them to `blockedPorts`. |

## The condition

Each project of an organization with limits has the condition `EgressLimitsApplied`. It never stops a project.

| Status | Reason | Meaning |
| --- | --- | --- |
| `True` | `Applied` | The limits are in force. The message says what is closed. |
| `False` | `NotEnforced` | The cluster does not enforce network isolation. Nothing holds the limits. |
| `Unknown` | `EnforcementUnknown` | The cluster was not checked yet, or the check could not tell. |
| `False` | `InvalidLimits` | Geass cannot apply the limits as written. The message says why. Each environment keeps what it had until you fix the limits. |

The dashboard does not show the condition yet. To read it, run `geass projects get morning-beach`. The condition is in `status.conditions` of the answer.

## Check the limits

From a service of the organization, try a mail port and a web port on a server that answers on both, for example your own mail server. The image of the service must have `nc`.

1. Open a service of a project of the organization, and select **Console**.
2. Select a copy in the **Pod** menu, and click **Connect**.
3. Run this command:

   ```bash
   for port in 25 443; do nc -z -w 5 mail.example.com $port && echo "$port open" || echo "$port closed"; done
   ```

With the limits in force, the command prints `25 closed` and `443 open`. Run the same command from a service of a project of no organization too. Many networks close port 25 themselves, so this shows if the server answers on 25 at all.

## What is not done

- **Detection of mining** or other abuse. See [Abuse detection](/docs/guides/abuse-detection/).
- **Limits on how much mail is sent.** The [mail relay](/docs/guides/mail-relay/) does this for the mail that goes through it.
- **Quotas for each destination**, and limits on traffic volume.
- **Destinations by host name.** The limits hold only addresses.
- **IPv6.** No IPv6 leaves the cluster, with or without the limits.
- **Traffic inside the cluster.** Anything in the cluster that forwards traffic, for example a relay or a proxy, is outside the limits.
- **DNS.** Data can still leave in DNS queries.
- **Shared database servers and object storage.** They belong to the platform, not to an organization.

## What has been tested

On real K3s, the limits closed the mail ports and the listed ranges, and left other ports open. An `allow` opened port 25 to one relay. Mail opened for one organization stayed closed for another. A later test on K3s v1.36.4+k3s1 confirmed the same with public mail servers. Only the network plugin of K3s was tested. Port 25 was not tested against a public host.

## Under the hood

### Set the limits on the server

The limits of the platform are in `spec.tenancy.egress` of the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/). When this section exists, mail is closed. All other fields are optional:

```yaml title="platform-egress.yaml"
spec:
  tenancy:
    egress:
      mail: Blocked               # the default: closes TCP 25, 465, 587, 2465, 2525 and 2587
      miningPools: Blocked        # closes the default ports of public mining pools (a heuristic)
      blockedPorts:               # more closed ports
        - port: 6666
        - port: 9999
      allow:                      # opened, whatever else closes them
        - cidr: 203.0.113.25/32
          ports:
            - port: 587
          description: the platform's mail relay
```

Do not apply the fragment alone. Patch it into the resource:

```bash
sudo k3s kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"tenancy":{"egress":{
  "mail":"Blocked",
  "miningPools":"Blocked",
  "blockedPorts":[{"port":6666},{"port":9999}],
  "allow":[{"cidr":"203.0.113.25/32","ports":[{"port":587}],"description":"the platform'"'"'s mail relay"}]}}}}'
```

This example opens only the web, HTTP/3, and outside databases. Nothing else leaves:

```yaml
spec:
  tenancy:
    egress:
      allowedPorts:
        - port: 80
        - port: 443
        - port: 443
          protocol: UDP
        - port: 5432
        - port: 3306
```

To remove the limits, run this command. Within a moment, every project is as it was before:

```bash
sudo k3s kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"tenancy":{"egress":null}}}'
```

The limits of one organization are in `spec.egress` of its [GeassOrganization](/docs/reference/resources/geass-organization/). This command lets one organization send mail:

```bash
sudo k3s kubectl -n geass-system patch geassorganization org-1a2b3c4d --type merge -p '{"spec":{"egress":{"mail":"Open"}}}'
```

To remove the limits of an organization, patch `{"spec":{"egress":null}}` on it.

To read the condition of a project on the server, run this command:

```bash
sudo k3s kubectl -n geass-system get geassproject morning-beach -o jsonpath='{.status.conditions[?(@.type=="EgressLimitsApplied")]}{"\n"}'
```

To check the limits from a namespace of an environment, without a service that has `nc`, run this command:

```bash
kubectl run probe --rm -it --restart=Never -n morning-beach-production --image=nicolaka/netshoot:v0.13 -- \
  sh -c 'for port in 25 443; do nc -z -w 5 mail.example.com $port && echo "$port open" || echo "$port closed"; done'
```

### What Geass writes

The limits are not a separate network policy. Policies only add permissions, so a second policy cannot close a port that another policy opens. Geass writes the limits into the policies that already decide where the services can go:

| Policy | Namespace | With the limits |
| --- | --- | --- |
| `geass-block-cloud-metadata` | Each environment of the projects of the organization | One rule for all pods of the cluster, on every port. One rule for `0.0.0.0/0`, minus the existing exceptions, on the open ports. One rule for each `allow`. |
| `geass-build-isolation` | `geass-build-<organization>` | The DNS rule. One rule for `0.0.0.0/0` minus the private ranges, on the open ports. One rule for each `allow`. |

Geass puts these policies back if someone edits them. A policy that a cluster administrator adds to the namespace of an organization adds to them, and can open what the limits close. To see the policies, run these commands:

```bash
kubectl -n morning-beach-production get networkpolicy geass-block-cloud-metadata -o yaml
kubectl -n geass-build-org-1a2b3c4d get networkpolicy geass-build-isolation -o yaml
```

The build namespace has no condition. If Geass cannot write its policy, the Geass log says so.

## Related

- [Network isolation](/docs/guides/network-isolation/)
- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/)
- [Mail relay](/docs/guides/mail-relay/): how organizations send mail after mail is closed.
- [Abuse detection](/docs/guides/abuse-detection/)
- [Noisy-neighbour limits](/docs/guides/tenant-limits/), [Sandboxed runtime for organizations](/docs/operations/sandboxed-runtime/), and [Registry isolation for organizations](/docs/operations/registry-isolation/): other settings for a cluster that organizations share.
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
- [GeassOrganization](/docs/reference/resources/geass-organization/)
- [Security](/docs/operations/security/)
