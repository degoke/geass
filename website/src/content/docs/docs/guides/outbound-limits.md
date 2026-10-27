---
title: "Outbound limits"
description: "Keep organizations sharing a cluster from sending mail straight to the internet, limit the ports their pods reach, and what a network policy cannot do against abuse."
---

On a cluster that several [organizations](/docs/guides/organizations-and-workspaces/) share, every organization's pods leave the cluster from the platform's addresses. A pod that sends spam straight to the internet's mail servers, or connects to a mining pool, gets those addresses blocked and reported, for everyone. **Outbound limits** close ports to addresses outside the cluster for the pods of organizations' projects: the ports mail is sent on by default, and optionally further ports, or all but a list of them.

They are off until the platform sets them. A Geass without organizations, or whose platform sets none, is unchanged: every policy is written exactly as before.

## What is limited, and how firmly

| Limit | Held by | Firm? |
| --- | --- | --- |
| **Mail sent straight out** (TCP 25, 465, 587, 2465, 2525 and 2587) | The network policy of each namespace the organization's pods run in. | **Enforced, if the network plugin enforces network policies** and their port ranges. Not on a plugin that ignores policies: the project's `EgressLimitsApplied` condition says which. |
| **Further closed ports** (`blockedPorts`) | The same policy. | The same. |
| **Only listed ports open** (`allowedPorts`) | The same policy. | The same. |
| **Destinations opened on closed ports** (`allow`), such as a mail relay | A rule of the same policy for each range of addresses. | The same. Addresses only: a policy cannot name a host. |
| **The cloud's metadata service, the cluster's machines, a build's private addresses** | What the policies already closed, before these limits. | As before. No setting of the limits opens them. |
| **Mining** | Nothing detects it. Closing the ports mining pools listen on is a heuristic, described in [Mining](#mining). | **Not enforced.** A miner who uses another port, port 443 or a proxy is not stopped. |
| **How much mail an organization sends** (rates, counts, per-destination quotas) | Nothing. | **Not done.** A network policy allows or refuses a connection; it cannot count. See [What is not done](#what-is-not-done). |

The limits apply in every namespace an organization's pods run in: each environment of each of its projects, pull request environments included, and its build namespace (`geass-build-<organization>`), since a build runs the organization's Dockerfile. A project that belongs to no organization is never limited, and neither is anything in the platform's namespaces (`geass-system`, `kube-system`, `monitoring` and the others).

## Before you start

- Organizations are set up, and projects are given to them (`spec.org`).
- Only a platform administrator can set the limits, with `kubectl` (or anything that writes the resources). No page or route of the dashboard changes them, the organization's own administrators included.
- **The network plugin has to enforce network policies, with port ranges.** K3s does, with its built-in network policy controller, unless it was started with `--disable-network-policy`. Geass checks that policies are enforced and each project says what it found (see [The condition](#the-condition)). The check does not try a port range (`endPort`, stable since Kubernetes 1.25): a plugin that ignores `endPort` applies only the first port of each range, which closes nearly every port instead of a few. **None of this has been run with Geass on a cluster whose plugin enforces it.** Try it on yours (see [Check the limits](#check-the-limits)) before you rely on it.
- **What it does to running workloads.** Nothing restarts. The policy changes at once, and new connections are held to it. Whether a connection that was already open is cut depends on the plugin: many let it go on until it closes.

## Set the limits

The platform's limits are in the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/), under `spec.tenancy.egress`. Setting the section at all closes mail; everything else is optional:

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
      blockedPorts:               # closed besides mail's: here, the ports mining pools listen on (a heuristic)
        - port: 3333
          endPort: 3336
        - port: 4444
        - port: 5555
        - port: 7777
        - port: 14433
        - port: 14444
        - port: 45700
      allow:                      # opened whatever else closes them
        - cidr: 203.0.113.25/32
          ports:
            - port: 587
          description: the platform's mail relay
```

The fragment shows only the limits. Patch them in rather than applying it alone:

```bash
kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"tenancy":{"egress":{
  "mail":"Blocked",
  "blockedPorts":[{"port":3333,"endPort":3336},{"port":4444},{"port":5555},{"port":7777},{"port":14433},{"port":14444},{"port":45700}],
  "allow":[{"cidr":"203.0.113.25/32","ports":[{"port":587}],"description":"the platform'"'"'s mail relay"}]}}}}'
```

To give one organization other limits, set `spec.egress` on its [GeassOrganization](/docs/reference/resources/geass-organization/). Each of `mail`, `allowedPorts`, `blockedPorts` and `allow` that it sets **replaces the platform's whole**; what it leaves out is the platform's. An empty `blockedPorts` or `allow` replaces the platform's with none. `allowedPorts` cannot be empty: to free one organization from the platform's list, give it every port, `[{"port":1,"endPort":65535},{"port":1,"endPort":65535,"protocol":"UDP"}]`. This is how one organization is let send mail that the others may not:

```bash
kubectl -n geass-system patch geassorganization org-1a2b3c4d --type merge -p '{"spec":{"egress":{"mail":"Open"}}}'
```

An organization's `spec.egress` limits it even when the platform sets nothing.

To remove them: `kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"tenancy":{"egress":null}}}'`, and the same with `{"spec":{"egress":null}}` on an organization. Geass writes each policy back as it was before the limits, within a moment.

| Field | Meaning |
| --- | --- |
| `mail` | `Blocked` (the default) closes the ports mail is sent and submitted on: TCP 25 (mail servers passing mail to each other, which is how spam goes straight to a mailbox's server), 465 and 587 (a mail program submitting mail, with TLS from the start or after STARTTLS), 2525 (the alternative many mail services offer where 587 is closed), and 2465 and 2587 (Amazon SES's alternatives to 465 and 587). `Open` leaves them open. Other alternatives some hosts use, such as 26 or 588, are not in the list: add them to `blockedPorts`. |
| `allowedPorts` | When set, the only destination ports open to addresses outside the cluster, and every other port is closed. At least one entry. List UDP ports too (such as 443, for HTTP/3), or no UDP leaves the cluster. A port that is allowed and also closed, by `mail` or `blockedPorts`, is closed. Unset means every port is open but the closed ones. |
| `blockedPorts` | Closed to addresses outside the cluster, besides mail's. |
| `allow[].cidr` | A range of IPv4 addresses, written from its first address, such as `203.0.113.25/32` for one address or `203.0.113.0/24`. Not in `169.254.0.0/16` or `127.0.0.0/8`. |
| `allow[].ports` | The ports opened to that range, whatever else closes them. At least one. |
| `allow[].description` | What the addresses are, for whoever reads the configuration. |
| A port: `port`, `endPort`, `protocol` | `port` is 1 to 65535. `endPort`, when set, makes it a range from `port` to `endPort`, both included, and must not be below `port`. `protocol` is `TCP` (the default) or `UDP`. |

Limits that Kubernetes would refuse are refused when they are saved: a port outside 1 to 65535, a range that ends before it starts, a protocol other than TCP and UDP, an empty `allowedPorts`, an allowance without ports, or a `cidr` that is not written like an address range. What only Geass can tell, because the platform's and an organization's limits are only put together there, is refused by the operator, which then leaves every policy as it was (see [The condition](#the-condition)): an allowance that is not a real IPv4 range (`999.0.0.0/8`), that does not start at the first address of its range (`203.0.113.25/24`), or that lies in the link-local or loopback range, and limits that leave no port open at all (an `allowedPorts` that lists only closed ports).

## Mail

With mail closed, an organization's pods cannot reach a mail server outside the cluster on any of the six ports. To let them send mail, open a way that the platform controls:

- **A relay in the cluster.** The cluster's own pods are reached on every port whatever the limits say, so a mail relay you run in a namespace of the platform's, such as `mail-relay`, is reachable from the organizations' pods on 587, and sends mail on from there, unlimited. That is where authentication, rate limits and per-organization accounting belong: the relay can do them and a network policy cannot.
- **An allowance** to a relay or provider outside the cluster, by its addresses: `allow: [{cidr: 203.0.113.25/32, ports: [{port: 587}]}]`. A mail service whose host name resolves to addresses that change cannot be named this way; use a relay.
- **`mail: Open` on one organization** that you trust to send mail.

## Ports

Without `allowedPorts`, every port is open but the closed ones. With it, only those it lists, less the closed ones: list what the organizations' services and builds need outside the cluster, such as 443 for registries, git hosts and S3 backups, the ports of outside databases, and a registry's own port when it is not 443. Two things stay open whatever the limits say:

- **The cluster's own pods, on every port.** That keeps the cluster's DNS, other services and databases working. It is also why a relay or proxy in the cluster is outside the limits (see [What is not done](#what-is-not-done)).
- **The cluster's API port, from project environments**, to any address: the port the cluster's `kubernetes` Service sends to, 6443 on K3s. The Postgres databases in a project's namespace use the API, which pods reach at the control plane's own address, and that address can be outside the nodes (a [hosted control plane](/docs/operations/hosted-control-plane/)). A build has no use for it, so in a build namespace it is limited like any other port.

A rule that lists ports leaves out every protocol it does not list. Once any port is closed, **ICMP (ping) and SCTP to addresses outside the cluster are closed too**: a network policy cannot name them. Limits that close nothing, such as `mail: Open` alone, leave the policy as it was.

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

Detecting mining takes watching what runs on the nodes: a program's CPU use and what it is, at runtime, with something like Falco or Tetragon. A network policy sees only addresses and ports, so **Geass detects no mining**. What a policy can do is close the ports public mining pools listen on by default, which stops a miner run with a pool's example configuration. It does nothing against one that connects to port 443 (many pools offer it), to any other port, or through a proxy, so treat it as a tripwire for the careless and not as a control.

A list to start from (it is not complete, pools change, and some of these ports have other uses):

| Ports | Why |
| --- | --- |
| 3333 to 3336, 4444, 5555, 7777 | The stratum ports many pools, and the examples of mining programs, use |
| 14433, 14444, 45700 | Further ports that published lists of mining pool ports name |
| 6666, 9999, 13333, 15555, 17777, 19999 | More pool ports, with more chance of other uses: 6666 is also IRC's, and other software listens on 9999. Add them knowing that |

Close them with `blockedPorts`, as in the example above. Use the cloud's own abuse signals and a runtime monitor for the rest.

## The condition

Each project of an organization with outbound limits carries the condition `EgressLimitsApplied`. It does not change `Ready`: the limits never hold a project back.

| Status | Reason | Meaning |
| --- | --- | --- |
| `True` | `Applied` | The limits are in the policy of every environment, and the cluster's network plugin enforces policies (the cluster's `NetworkPolicyEnforced` condition is `True`). The message says what is closed, or what is open with `allowedPorts`. |
| `False` | `NotEnforced` | The limits are written, but the cluster's check found that its network plugin does not enforce network policies. Nothing holds them. |
| `Unknown` | `EnforcementUnknown` | The limits are written, but the cluster has not been checked yet, or the last check could not tell. |
| `False` | `InvalidLimits` | The limits cannot be applied as they are written. Each environment keeps the policy it had, limits and all, until they are fixed; the message says what is wrong. An environment made meanwhile gets its policy without the limits, and an edit to a policy is not put back meanwhile. |

A project of no organization, or of an organization without limits, has no such condition. Read it with:

```bash
kubectl -n geass-system get geassproject morning-beach -o jsonpath='{.status.conditions[?(@.type=="EgressLimitsApplied")]}{"\n"}'
```

The build namespace has no condition of its own. A problem writing its policy is in the operator's log.

## What Geass writes

The limits are not a policy of their own. A network policy only ever allows, and a pod may open any connection that one of the policies selecting it allows, so a second policy could not close a port that `geass-block-cloud-metadata` opens. Geass writes them into the policies that already decide where the pods may go, and splits their rule for the outside world:

| Policy | Namespace | With the limits |
| --- | --- | --- |
| `geass-block-cloud-metadata` | Each environment of the organization's projects | One rule for every pod of the cluster, on every port. One rule for `0.0.0.0/0` less the same exceptions as before (`169.254.0.0/16`, and the nodes when the project keeps away from them), on the open ports, written as the fewest port ranges (`port` and `endPort`), TCP then UDP. One rule for each allowance, less the same exceptions; an allowance that lies inside one opens nothing and is left out. The rule that keeps the API open on the control plane, when there is one, is unchanged. |
| `geass-build-isolation` | `geass-build-<organization>` | The rule for the cluster's DNS, unchanged. One rule for `0.0.0.0/0` less the private ranges, on the open ports. One rule for each allowance, less the private ranges. |

The project controller keeps the first, and the organization controller (and each build that starts) the second. A policy that is edited or deleted by hand is put back. Geass never writes or deletes any other policy for this. A policy someone else adds to a tenant namespace **adds to Geass's**, and can open what the limits close: the organizations cannot add one, but a cluster administrator can.

## Check the limits

The policy in force:

```bash
kubectl -n morning-beach-production get networkpolicy geass-block-cloud-metadata -o yaml
kubectl -n geass-build-org-1a2b3c4d get networkpolicy geass-build-isolation -o yaml
```

To see it hold, try a mail port and a web port from a pod in one of the organization's environments. Use an address that answers on both, such as a mail server you run:

```bash
kubectl run probe --rm -it --restart=Never -n morning-beach-production --image=nicolaka/netshoot:v0.13 -- \
  sh -c 'for port in 25 443; do nc -z -w 5 mail.example.com $port && echo "$port open" || echo "$port closed"; done'
```

With the limits in force it prints `25 closed` and `443 open`. Run the same from a namespace that is not an organization's, such as `default`, to see that the server answers on 25 at all: many networks close 25 on their own.

## What is not done

- **Detecting mining**, or any other abuse. See [Mining](#mining).
- **Limiting how much mail is sent**, as opposed to closing the ports: rates, counts, per-organization accounting. A relay in the cluster is where that belongs.
- **Quotas per destination**, or on traffic volume. Bandwidth per pod is a different limit, which needs the network plugin's bandwidth support.
- **Naming destinations by host name.** A network policy holds addresses only.
- **IPv6.** Geass's policies let only IPv4 leave the cluster, so IPv6 to outside addresses was closed before the limits and stays closed.
- **Traffic inside the cluster.** A pod reaches every pod of the cluster on every port, so anything in the cluster that forwards traffic (a relay, a proxy, an egress gateway, an organization that was given mail and opens its project to the others) is outside the limits and has to limit what it forwards itself.
- **DNS.** Pods resolve through the cluster's DNS, which is never limited, so data can still leave in DNS queries.
- **The platform's namespaces and shared servers.** Shared database servers and object storage run in `geass-system` and are not an organization's.

## What has been tested

The policies are tested port by port: a test evaluates each one, for every port from 1 to 65535 of TCP and UDP and for addresses on the internet, on a private network, at the metadata service, at a node and at a pod, with a reading of the NetworkPolicy rules written apart from the code that builds them, and compares that with what the limits mean. The controllers are tested against fake clients: the limits are written into an organization's environments and build namespace and nowhere else, follow changes and go with the setting, are put back after an edit, and leave everything as it was when they cannot be applied. Against a real API server (envtest), the CRDs refuse what they should, and the policies are accepted and stored exactly as written.

**On a real K3s**, real traffic went through them, from a pod of each of two organizations to listeners on the host the cluster ran on: with no limits every port answered; with them the mail ports and a closed range were refused while the port after the range, 443 and 8080 answered (so K3s's network policy controller applies `endPort` ranges), an allowance opened port 25 to one relay, mail opened for one organization left the other's closed, and removing the setting opened everything again. The project said `EnforcementUnknown` until the cluster's own check of network policy enforcement had run, then `True`. Only one cluster and one network plugin (K3s's own) have been tried.

## Related

- [Network isolation](/docs/guides/network-isolation/)
- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/)
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
- [GeassOrganization](/docs/reference/resources/geass-organization/)
- [Security](/docs/operations/security/)
- [Noisy-neighbour limits](/docs/guides/tenant-limits/), [Sandboxed runtime for organizations](/docs/operations/sandboxed-runtime/) and [Registry isolation for organizations](/docs/operations/registry-isolation/), the other settings for a cluster that organizations share
