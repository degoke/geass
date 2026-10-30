---
title: "Outbound limits"
description: "Stop organizations from sending mail directly to the internet, limit the ports that their pods reach, and learn what a network policy cannot stop."
---

On a cluster that several [organizations](/docs/guides/organizations-and-workspaces/) share, the pods of every organization leave the cluster from the addresses of the platform. A pod can send spam directly to the mail servers of the internet, or connect to a mining pool. Then other parties block and report those addresses, for everyone. **Outbound limits** close ports to addresses outside the cluster for the pods of the projects of organizations. They close these ports:

- The ports that mail uses by default.
- Optionally, more ports, or all ports except a list of ports.

The limits are off until the platform sets them. If Geass has no organizations, or if the platform sets no limits, nothing changes: Geass writes every policy exactly as before.

## What is limited, and how firmly

| Limit | Held by | Firm? |
| --- | --- | --- |
| **Mail sent straight out** (TCP 25, 465, 587, 2465, 2525, and 2587) | The network policy of each namespace that the pods of the organization run in. | **Enforced, if the network plugin enforces network policies** and their port ranges. Not enforced on a plugin that ignores policies. The `EgressLimitsApplied` condition of the project tells you which. |
| **Further closed ports** (`blockedPorts`) | The same policy. | The same. |
| **Only listed ports open** (`allowedPorts`) | The same policy. | The same. |
| **Destinations opened on closed ports** (`allow`), such as a mail relay | A rule of the same policy for each range of addresses. | The same. Addresses only: a policy cannot name a host. |
| **The cloud's metadata service, the cluster's machines, a build's private addresses** | What the policies already closed, before these limits. | As before. No setting of the limits opens them. |
| **Mining** | Nothing here detects mining. To close the ports that mining pools listen on (`miningPools: Blocked`, or your own `blockedPorts`) is a heuristic. [Mining](#mining) describes it. [Abuse detection](/docs/guides/abuse-detection/) is the feature that looks for mining. | **Not enforced.** The limits do not stop a miner that uses a different port, port 443, or a proxy. |
| **How much mail an organization sends** (rates, counts, per-destination quotas) | Not the outbound limits. A network policy allows or refuses a connection, and it cannot count. When the platform sets up the [mail relay](/docs/guides/mail-relay/), the relay limits the messages, recipients, message size, and connections of each organization. Nothing sets quotas for each destination. | **Not by these limits.** The relay holds its limits for the mail that goes through the relay. See [What is not done](#what-is-not-done). |

The limits apply in every namespace that the pods of an organization run in:

- Each environment of each project of the organization, pull request environments included.
- The build namespace of the organization (`geass-build-<organization>`), because a build runs the Dockerfile of the organization.

Geass never limits a project that belongs to no organization. Geass also never limits anything in the namespaces of the platform (`geass-system`, `kube-system`, `monitoring`, and the others).

## Before you start

- Organizations are set up, and projects are given to them (`spec.org`).
- Only a platform administrator can set the limits, with `kubectl` (or any tool that writes the resources). No page or route of the dashboard changes the limits. This rule includes the administrators of the organization.
- **The network plugin must enforce network policies, with port ranges.** K3s enforces them with its built-in network policy controller, unless K3s was started with `--disable-network-policy`. Geass checks that the plugin enforces policies, and each project tells you what Geass found (see [The condition](#the-condition)). The check does not try a port range (`endPort`, stable since Kubernetes 1.25). A plugin that ignores `endPort` applies only the first port of each range. Such a plugin closes almost every port instead of a few ports. The limits were tested with Geass only on the K3s controller (see [What has been tested](#what-has-been-tested)). Before you rely on the limits, try them on your plugin (see [Check the limits](#check-the-limits)).
- **What the limits do to running workloads.** Nothing restarts. The policy changes immediately, and new connections must obey it. The plugin decides if it cuts a connection that was already open. Many plugins let the connection continue until it closes.

## Set the limits

The limits of the platform are in the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/), under `spec.tenancy.egress`. When you set this section at all, it closes mail. All other fields are optional:

```yaml title="platform-egress.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassPlatformConfig
metadata:
  name: platform
  namespace: geass-system
spec:
  tenancy:
    egress:
      mail: Blocked               # the default: closes TCP 25, 465, 587, 2465, 2525 and 2587
      miningPools: Blocked        # closes the ports public mining pools listen on by default (a heuristic)
      blockedPorts:               # closed besides those: here, more pools' ports, knowing they have other uses
        - port: 6666
        - port: 9999
      allow:                      # opened whatever else closes them
        - cidr: 203.0.113.25/32
          ports:
            - port: 587
          description: the platform's mail relay
```

The fragment shows only the limits. Patch the limits into the resource. Do not apply the fragment alone:

```bash
kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"tenancy":{"egress":{
  "mail":"Blocked",
  "miningPools":"Blocked",
  "blockedPorts":[{"port":6666},{"port":9999}],
  "allow":[{"cidr":"203.0.113.25/32","ports":[{"port":587}],"description":"the platform'"'"'s mail relay"}]}}}}'
```

To give one organization different limits, set `spec.egress` on its [GeassOrganization](/docs/reference/resources/geass-organization/). The organization can set `mail`, `miningPools`, `allowedPorts`, `blockedPorts`, and `allow`:

- Each of these fields that the organization sets **replaces the whole platform value** of that field.
- Each field that the organization does not set comes from the platform.
- An empty `blockedPorts` or `allow` replaces the platform value with none.
- `allowedPorts` cannot be empty. To free one organization from the platform list, give the organization every port: `[{"port":1,"endPort":65535},{"port":1,"endPort":65535,"protocol":"UDP"}]`.

This command lets one organization send mail that the other organizations cannot send:

```bash
kubectl -n geass-system patch geassorganization org-1a2b3c4d --type merge -p '{"spec":{"egress":{"mail":"Open"}}}'
```

The `spec.egress` of an organization limits that organization even when the platform sets nothing.

To remove the limits of the platform, run `kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"tenancy":{"egress":null}}}'`. For an organization, run the same command with `{"spec":{"egress":null}}` on the organization. Within a moment, Geass writes each policy back as it was before the limits.

| Field | Meaning |
| --- | --- |
| `mail` | `Blocked` (the default) closes the ports that mail is sent and submitted on: TCP 25 (mail servers that pass mail to each other, which is how spam goes directly to the server of a mailbox), 465 and 587 (a mail program that submits mail, with TLS from the start or after STARTTLS), 2525 (the alternative that many mail services offer where 587 is closed), and 2465 and 2587 (the Amazon SES alternatives to 465 and 587). `Open` leaves these ports open. Other alternatives that some hosts use, such as 26 or 588, are not in the list. Add them to `blockedPorts`. |
| `miningPools` | `Blocked` closes a curated list of the TCP ports that public mining pools, and the example configurations of mining programs, listen on by default: 3333 to 3336, 4444, 5555, and 7777, and 14433, 14444, and 45700. `Open`, or no value, leaves these ports open. This is a heuristic: see [Mining](#mining). Like `mail`, it closes these ports to every address except the addresses that `allow` opens them to. |
| `allowedPorts` | When set, the only destination ports open to addresses outside the cluster. Every other port is closed. At least one entry. Also list UDP ports (for example 443, for HTTP/3), or no UDP leaves the cluster. A port that is allowed and also closed, by `mail` or `blockedPorts`, is closed. When unset, every port is open except the closed ports. |
| `blockedPorts` | Ports closed to addresses outside the cluster, in addition to the mail ports. |
| `allow[].cidr` | A range of IPv4 addresses, written from its first address, such as `203.0.113.25/32` for one address or `203.0.113.0/24`. Not in `169.254.0.0/16` or `127.0.0.0/8`. |
| `allow[].ports` | The ports opened to that range, whatever else closes them. At least one. |
| `allow[].description` | What the addresses are, for the people who read the configuration. |
| A port: `port`, `endPort`, `protocol` | `port` is 1 to 65535. `endPort`, when set, makes a range from `port` to `endPort`, both included. `endPort` must not be below `port`. `protocol` is `TCP` (the default) or `UDP`. |

When you save the limits, Kubernetes refuses the limits that it would not accept:

- A port outside 1 to 65535.
- A range that ends before it starts.
- A protocol other than TCP and UDP.
- An empty `allowedPorts`.
- An allowance without ports.
- A `cidr` that is not written like an address range.

Only Geass can find some problems, because only Geass puts together the limits of the platform and of an organization. The operator refuses these limits and leaves every policy as it was (see [The condition](#the-condition)):

- An allowance that is not a real IPv4 range (`999.0.0.0/8`).
- An allowance that does not start at the first address of its range (`203.0.113.25/24`).
- An allowance in the link-local or loopback range.
- Limits that leave no port open at all, for example an `allowedPorts` that lists only closed ports.

## Mail

When mail is closed, the pods of an organization cannot reach a mail server outside the cluster on any of the six ports. To let the pods send mail, open a path that the platform controls:

- **The Geass mail relay** (`spec.tenancy.mail.relay`). This guide recommends this path. See [Mail relay](/docs/guides/mail-relay/). The relay works like this:
  - Geass runs the relay in `geass-system`.
  - Geass gives each organization its own login: the Secret `geass-mail` in each environment of the organization.
  - The relay holds each organization to its limits: messages per minute, hour, and day, recipients, size, connections, and sender domains.
  - The relay sends the mail to the upstream service that you select.
  - Pods can reach the pods of the cluster on every port, whatever the limits say. Thus, the organizations reach the relay on 587 when mail is closed, and when `allowedPorts` does not list 587. The relay needs no `allow` entry.
- **Your own relay in the cluster**, in a namespace of the platform. Pods reach it the same way. It sends on all the mail that it gets, with no limit, unless it does its own authentication and counting.
- **An allowance** to a relay or provider outside the cluster, by its addresses: `allow: [{cidr: 203.0.113.25/32, ports: [{port: 587}]}]`. You cannot name a mail service this way if its host name resolves to addresses that change. Use a relay for such a service.
- **`mail: Open` on one organization** that you trust to send mail.

## Ports

Without `allowedPorts`, every port is open except the closed ports. With `allowedPorts`, only the listed ports are open, minus the closed ports. List the ports that the services and builds of the organizations need outside the cluster. For example:

- 443 for registries, git hosts, and S3 backups.
- The ports of outside databases.
- The port of a registry, when it is not 443.

Two things stay open, whatever the limits say:

- **The pods of the cluster, on every port.** Thus, the DNS, other services, and databases of the cluster continue to work. For the same reason, a relay or proxy in the cluster is outside the limits (see [What is not done](#what-is-not-done)). Also for this reason, the [Geass mail relay](/docs/guides/mail-relay/) needs no place in `allowedPorts`.
- **The API port of the cluster, from project environments**, to any address. This is the port that the `kubernetes` Service of the cluster sends to: 6443 on K3s. The Postgres databases in the namespace of a project use the API. Pods reach the API at the address of the control plane itself, and that address can be outside the nodes (a [hosted control plane](/docs/operations/hosted-control-plane/)). A build does not use the API. Thus, in a build namespace, the limits apply to this port like any other port.

A rule that lists ports leaves out every protocol that it does not list. After any port is closed, **ICMP (ping) and SCTP to addresses outside the cluster are closed too**, because a network policy cannot name them. Limits that close nothing, such as `mail: Open` alone, leave the policy as it was.

```yaml
spec:
  tenancy:
    egress:
      allowedPorts:      # the web, HTTP/3, and outside databases; nothing else leaves
        - port: 80
        - port: 443
        - port: 443
          protocol: UDP
        - port: 5432
        - port: 3306
```

## Mining

To detect mining, a tool must watch what runs on the nodes at runtime: what a program is, and how much CPU it uses. A network policy sees only addresses and ports, so **these limits detect no mining**. [Abuse detection](/docs/guides/abuse-detection/) is the feature that looks for mining. It uses the CPU that a service uses, and the reports of a tool such as Falco or Tetragon.

A policy can close the ports that public mining pools listen on by default. This stops a miner that runs with the example configuration of a pool. It does nothing against a miner that connects to port 443 (many pools offer it), to any other port, or through a proxy. Thus, treat this setting as a tripwire for careless miners, not as a control.

`miningPools: Blocked` closes the first two rows of this list. If you accept the other uses of the third row, add it yourself with `blockedPorts`. The list is not complete, and pools change:

| Ports | Why |
| --- | --- |
| 3333 to 3336, 4444, 5555, 7777 | The stratum ports that many pools, and the examples of mining programs, use |
| 14433, 14444, 45700 | More ports that published lists of mining pool ports name |
| 6666, 9999, 13333, 15555, 17777, 19999 | More pool ports, with a higher chance of other uses: IRC also uses 6666, and other software listens on 9999. Add them only if you accept that risk |

Close these ports with `miningPools` and `blockedPorts`, as in the example above. For the rest, use [abuse detection](/docs/guides/abuse-detection/), the abuse signals of the cloud, and a runtime monitor.

## The condition

Each project of an organization with outbound limits has the condition `EgressLimitsApplied`. This condition does not change `Ready`: the limits never stop a project.

| Status | Reason | Meaning |
| --- | --- | --- |
| `True` | `Applied` | The limits are in the policy of every environment, and the network plugin of the cluster enforces policies (the `NetworkPolicyEnforced` condition of the cluster is `True`). The message tells you what is closed, or what is open with `allowedPorts`. |
| `False` | `NotEnforced` | Geass wrote the limits, but the check of the cluster found that its network plugin does not enforce network policies. Nothing holds the limits. |
| `Unknown` | `EnforcementUnknown` | Geass wrote the limits, but the cluster is not checked yet, or the last check could not tell. |
| `False` | `InvalidLimits` | Geass cannot apply the limits as they are written. Each environment keeps the policy that it had, limits included, until you fix the limits. The message tells you what is wrong. Meanwhile, an environment that Geass makes gets its policy without the limits, and Geass does not put back an edit to a policy. |

A project of no organization, or of an organization without limits, does not have this condition. To read the condition, run:

```bash
kubectl -n geass-system get geassproject morning-beach -o jsonpath='{.status.conditions[?(@.type=="EgressLimitsApplied")]}{"\n"}'
```

The build namespace has no condition of its own. If Geass cannot write its policy, the problem is in the log of the operator.

## What Geass writes

The limits are not a separate policy. A network policy only allows. A pod can open any connection that one of the policies that select it allows. Thus, a second policy could not close a port that `geass-block-cloud-metadata` opens. Geass writes the limits into the policies that already decide where the pods can go, and it splits their rule for the outside world:

| Policy | Namespace | With the limits |
| --- | --- | --- |
| `geass-block-cloud-metadata` | Each environment of the projects of the organization | One rule for every pod of the cluster, on every port. One rule for `0.0.0.0/0` minus the same exceptions as before (`169.254.0.0/16`, and the nodes when the project keeps away from them), on the open ports. Geass writes this rule as the fewest port ranges (`port` and `endPort`), TCP then UDP. One rule for each allowance, minus the same exceptions. Geass leaves out an allowance that lies inside an exception, because it opens nothing. The rule that keeps the API open on the control plane, when there is one, does not change. |
| `geass-build-isolation` | `geass-build-<organization>` | The rule for the DNS of the cluster, unchanged. One rule for `0.0.0.0/0` minus the private ranges, on the open ports. One rule for each allowance, minus the private ranges. |

The project controller keeps the first policy. The organization controller, and each build that starts, keep the second policy. If someone edits or deletes a policy by hand, Geass puts it back. Geass never writes or deletes any other policy for the limits. A policy that someone else adds to a tenant namespace **adds to the Geass policies**, and it can open what the limits close. The organizations cannot add a policy, but a cluster administrator can.

## Check the limits

To see the policy in force, run:

```bash
kubectl -n morning-beach-production get networkpolicy geass-block-cloud-metadata -o yaml
kubectl -n geass-build-org-1a2b3c4d get networkpolicy geass-build-isolation -o yaml
```

To see the policy hold, try a mail port and a web port from a pod in one of the environments of the organization. Use an address that answers on both ports, for example a mail server that you run:

```bash
kubectl run probe --rm -it --restart=Never -n morning-beach-production --image=nicolaka/netshoot:v0.13 -- \
  sh -c 'for port in 25 443; do nc -z -w 5 mail.example.com $port && echo "$port open" || echo "$port closed"; done'
```

When the limits are in force, the command prints `25 closed` and `443 open`. Run the same command from a namespace that does not belong to an organization, for example `default`. This shows if the server answers on 25 at all, because many networks close 25 themselves.

## What is not done

- **Detection of mining**, or of any other abuse. A network policy sees ports, not programs. See [Mining](#mining) and [Abuse detection](/docs/guides/abuse-detection/).
- **Limits on how much mail is sent**, as opposed to closed ports: rates, counts, and accounting for each organization. A network policy cannot count. The [mail relay](/docs/guides/mail-relay/) does this for the mail that goes through it.
- **Quotas for each destination**, or on traffic volume. Bandwidth for each pod is a different limit, and it needs the bandwidth support of the network plugin.
- **Destinations named by host name.** A network policy holds only addresses.
- **IPv6.** The Geass policies let only IPv4 leave the cluster. Thus, IPv6 to outside addresses was closed before the limits and stays closed.
- **Traffic inside the cluster.** A pod reaches every pod of the cluster on every port. Thus, anything in the cluster that forwards traffic is outside the limits, and it must limit what it forwards itself. Examples are a relay, a proxy, an egress gateway, and an organization that has mail open and opens its project to the others.
- **DNS.** Pods resolve names through the DNS of the cluster, which is never limited. Thus, data can still leave in DNS queries.
- **The namespaces and shared servers of the platform.** Shared database servers and object storage run in `geass-system`, and they do not belong to an organization.

## What has been tested

The policies are tested port by port, `miningPools` included, against the list above written out port by port. A test evaluates each policy for these cases:

- Every port from 1 to 65535, of TCP and UDP.
- Addresses on the internet, on a private network, at the metadata service, at a node, and at a pod.

The test uses a reading of the NetworkPolicy rules that is written separately from the code that builds the rules. It compares that reading with what the limits mean.

The controllers are tested against fake clients. The tests show these results:

- Geass writes the limits into the environments and build namespace of an organization, and nowhere else.
- The limits follow changes, and they go away with the setting.
- Geass puts the limits back after an edit.
- Geass leaves everything as it was when it cannot apply the limits.

Against a real API server (envtest), the CRDs refuse what they should refuse. The API server accepts the policies and stores them exactly as written.

**On a real K3s**, real traffic went through the policies. The traffic went from a pod of each of two organizations to listeners on the host that the cluster ran on. The results were:

- With no limits, every port answered.
- With the limits, the mail ports and a closed range were refused. The port after the range, 443, and 8080 answered. Thus, the K3s network policy controller applies `endPort` ranges.
- An allowance opened port 25 to one relay.
- Mail opened for one organization left mail closed for the other organization.
- When the setting was removed, everything opened again.

The project said `EnforcementUnknown` until the cluster ran its own check of network policy enforcement. Then the project said `True`. Only one network plugin (the K3s plugin) has been tried.

The next test used K3s v1.36.4+k3s1, with `spec.tenancy.egress: {}` (mail closed), and traffic to public addresses over IPv4. The results were:

- From a pod of an organization, `smtp.gmail.com` refused ports 587 and 465. The pod reached `imap.gmail.com` port 993, which is not a mail port.
- From a pod of no organization, all three were reached.
- After `spec.egress.mail: Open` on the organization, its pod reached 587 and 465.
- The policy of the build namespace of the organization allowed these TCP ranges to addresses outside the private ranges: `1-24`, `26-464`, `466-586`, `588-2464`, `2466-2524`, `2526-2586`, and `2588-65535`. That is, all ports except 25, 465, 587, 2465, 2525, and 2587.
- An `allow` for a public address on two ports opened those ports for the build namespace of the organization.
- An `allow` for a private address did not open anything. The private-range block stays, as the table above says.

Port 25 was not tried against a public host, because AWS throttles port 25 for every instance.

## Related

- [Network isolation](/docs/guides/network-isolation/)
- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/)
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
- [GeassOrganization](/docs/reference/resources/geass-organization/)
- [Security](/docs/operations/security/)
- [Noisy-neighbour limits](/docs/guides/tenant-limits/), [Sandboxed runtime for organizations](/docs/operations/sandboxed-runtime/), [Registry isolation for organizations](/docs/operations/registry-isolation/), and [Abuse detection](/docs/guides/abuse-detection/): the other settings for a cluster that organizations share.
- [Mail relay](/docs/guides/mail-relay/): the way that organizations send mail after mail is closed.
