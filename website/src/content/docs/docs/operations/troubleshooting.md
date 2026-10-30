---
title: "Troubleshooting"
description: "Fixes by symptom for installs, sign-in, nodes, stuck services and databases, deploys, builds, domains, certificates, backups, volumes, and full clusters."
---

This page is organized by what you see. Each entry tells you where to look in the dashboard and with `kubectl`, what
usually causes the problem, and how to fix it. Messages in quotation marks are the exact text of the dashboard or of a
condition. Each entry tells you which one. The dashboard changes many condition messages into plain language, so often
only `kubectl` shows the exact text.

## First, see what is wrong

### Where things live

| What | Where |
| --- | --- |
| The operator and dashboard | Deployment `geass-controller-manager` in namespace `geass-system`. The dashboard is part of the operator. It listens on port 8085 behind the service `geass-dashboard`. |
| Geass resources (`GeassApp`, `GeassDatabase`, `GeassProject`, `GeassCluster`, `GeassNode`, `GeassNodePool`, `GeassBuild`, and others) | Namespace `geass-system` |
| The pods, services, and ingresses of a project environment | Namespace `<project>-<environment>`, for example `morning-beach-production` |
| Add-ons | `cert-manager`, `monitoring`, `longhorn-system`, `cnpg-system`, `moco-system`, `mariadb-system`, `geass-logging`, and, when it is turned on, `keda` |
| Helm installs of add-ons | `HelmChart` objects in `kube-system`, installed by `helm-install-<name>` jobs |

Run the `kubectl` commands on the control plane as `sudo k3s kubectl ...`, or set `KUBECONFIG=/etc/rancher/k3s/k3s.yaml`.
This page writes `kubectl`.

```bash
kubectl -n geass-system get pods
kubectl -n geass-system logs deploy/geass-controller-manager -c manager --tail=200
kubectl -n geass-system get geasscluster default -o jsonpath='{.status.conditions}{"\n"}'
```

### What the status labels mean

A service, database, or bucket shows one of these labels. The dashboard gets the label from the `Ready` condition of
the resource: its reason and its message.

| Label | What it means | What to do |
| --- | --- | --- |
| **Draft** | The resource was created in the dashboard or through its API, and it is not deployed yet. The condition reads "Service is configured and waiting for deployment" (or "Resource is configured…"). If you applied a service from a manifest without `deploy.enabled: true`, the service has no draft marker. So the dashboard shows it as **Unavailable** instead. | Click **Deploy changes**. |
| **Setting up** | A new resource that has no condition yet, and the few messages that count as progress: "Service is starting", "Postgres database is starting" (any "… database is starting"), and "Installing … chart". | Wait. If the label stays, see [A service stays "Setting up"](#a-service-stays-setting-up). |
| **Unavailable** | The label for every other not-ready condition that is not a failure. It includes real shortages, for example capacity, object storage, an AWS connection, or a project that is not ready. It also includes plain waiting: "Storage is starting", "Git source is waiting for a successful build", "Restoring database from backup", and "High availability requires …". The label does not show the difference between the two. | Read the condition with `kubectl`, as shown below. Fix a shortage. For a waiting message, wait. |
| **Failed** | Geass stopped trying. The reason is `ProvisionFailed` ("Provisioning failed after repeated errors. Update settings or platform configuration, then save or redeploy to try again."), or `RolloutFailed`. A service gets `RolloutFailed` only when a manifest sets `spec.deploy.failureThreshold`. The dashboard never sets it. | For `ProvisionFailed`, fix the cause, and then click **Retry**. See below. |
| **Deleting** | Geass is removing the resource. | Wait. See [A database stays "Deleting"](#a-database-stays-deleting). |
| **Ready** | The resource is running. | None. |

The line under the label is also in plain language, for example "This database isn't available yet. Check Platform
settings or your configuration." The entries on this page quote the text of the condition itself. To read that text,
run this command:

```bash
kubectl -n geass-system get geassapp web \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}{"\n"}'
```

### How Geass gives up, and how to start again

While a resource is not ready, Geass tries again every 30 seconds. Geass records the reason in the `Ready` condition.
Most reasons count as failures. A resource becomes **Failed** in one of these cases:

- After five failed passes in a row.
- After three minutes of failures.
- Immediately, for an error that cannot heal itself, for example an `access denied` or an unsupported engine.

If the server of a database cannot pull its image, Geass gives the database ten minutes instead, because the kubelet
waits minutes between pulls. Waiting states never count as failures. These are the waiting states:

- A draft.
- Pending changes.
- A build in progress.
- An install in progress.
- "is starting".
- A restore.
- "High availability requires …".
- A service that sleeps when idle and is starting after a request.

A failed resource stays paused until its settings change, or until you click **Retry** on its page. Geass then shows
"Retrying provisioning". **Retry** appears only on a resource whose reason is `ProvisionFailed`. Fix the cause first.
If you do not, the resource fails again in about two minutes.

**Deploy history** in the dashboard and the final condition hide the raw error, with one exception. Geass keeps a
message that says a service "cannot start", because that message says what is wrong with the service itself. See
[A service stays "Setting up"](#a-service-stays-setting-up). To read the real cause, click **Retry**. Then read the
condition during the next minutes, before Geass gives up again:

```bash
kubectl -n geass-system get geassapp web \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")]}{"\n"}'
kubectl -n geass-system get geassapp web -w \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}{"\n"}'
```

Use `geassdatabase`, `geassobjectstore`, or `geasslogicaldatabase` for the other kinds. For **Details**, **Build Logs**,
and **Deploy Logs**, open the **Deployments** tab of the service, and then open a deployment in it.

## Install

**The install script prints "Could not download Geass from …".**
The download failed. The line above the message is the error from curl itself. For example,
`curl: (22) The requested URL returned error: 404` is for a version or file that does not exist.
`curl: (6) Could not resolve host` is for no route to GitHub. Make sure that these conditions are true:

- A `GEASS_VERSION` that you pinned is a release, and the release has a file for your architecture.
- The server can reach `github.com` over HTTPS or through your proxy.

On any architecture other than x86-64 and arm64, the script exits with "unsupported architecture" before it downloads
anything. The script deletes its temporary file when it ends.

**"unsupported architecture", "insufficient memory: … (a server with 2 GB is needed …)" or "insufficient disk space on /: … (10240 MB required)".**
The **system checks** step stops at the first requirement that the machine does not meet. The requirements are:

- Linux
- amd64 or arm64
- 2 GB of memory for a server: at least 1700 MiB of `MemTotal`, which a machine sold as 2 GB reports
- 10 GB free on `/`

See [Requirements](/docs/getting-started/requirements/).

**An install step fails.**
The installer prints each step with `→`. It prints a `✓` when the step finishes, and `✗ <step> failed: <reason>` when
the step stops. Then it prints "Detailed log: <path>". The log is `geass-install-<UTC date and time>.log` in the
current directory, or at `GEASS_INSTALL_LOG`. If the first place is not writable, the log is in the system temp
directory. To run the K3s installer with `sh -x`, set `GEASS_INSTALL_TRACE=1`. These are the usual failures:

| Step | Message | Cause and fix |
| --- | --- | --- |
| **system checks** | `GEASS_ACME_EMAIL "<value>" is not an email address` | The value does not look like an address, or it is longer than 254 characters. The check runs before the installer changes anything. Fix the value, or leave out the variable. |
| **wait for k3s ready** | "k3s did not become ready within 5 minutes" | K3s did not start. Read `journalctl -u k3s -n 100`. Common causes are a port that is already in use, or too little memory. |
| **remove existing installation** | "existing k3s installation detected but no uninstall script found …" | Only a `--reinstall` gets to this step. Geass cannot remove this K3s install. Remove it yourself, and then run the installer again. |
| **check the cluster before updating** | "the cluster is not in a state to be updated", "does not downgrade", "this release cannot replace the custom resource definitions …" | The update found a reason not to change the cluster. Nothing was changed. See [What stops an update](/docs/operations/upgrading/#what-stops-an-update). |
| **wait for the new operator to take over** | "the new operator did not report release …", "the operator pod is not starting: …" | The new operator did not start, or it refused to start. The installer prints the log of the operator. When it is safe, the installer rolls the operator back to the one that ran before, and leaves the cluster on that operator. See [When the new operator does not take over](/docs/operations/upgrading/#when-the-new-operator-does-not-take-over). |
| **wait for operator ready** | "the operator did not become ready within 5m0s", or "the operator pod is not starting: ..." when its pod is crash looping or cannot pull its image | Run `kubectl -n geass-system describe pod -l control-plane=controller-manager` and read the events. |
| **apply GeassCluster CR** | "wait for GeassCluster CRD: …" | The custom resource definitions of the operator did not become established in 60 seconds. Check the operator pod. |
| **store worker join token** | "read join token: …" or "join token file … is empty" | K3s has not written `/var/lib/rancher/k3s/server/node-token`. Check K3s, and then run the installer again. |

In multi-provider mode, see [Nodes at more than one provider](/docs/operations/multi-provider/) for the messages about
`GEASS_NODE_EXTERNAL_IP` and WireGuard. For a mesh (`spec.network.mesh`), see
[Mesh networking](/docs/operations/mesh-networking/#troubleshooting). That page covers the messages that begin "the mesh
cannot take", and the `MeshReady` condition. The condition includes `OverlayUnreachable`: a probe from the operator got
no answer from the pod network of a node. The overlay check ran one time on EC2, and it also gave false reports there.
Before you trust `OverlayUnreachable`, read what that run found.

:::danger
Reinstall only a machine that you mean to rebuild. The installer removes a K3s only when you pass `--reinstall`, after
a confirmation. On a machine that already runs Geass, the installer updates Geass and keeps what the machine holds. On
any other machine that has K3s, the installer stops and says why.
:::

**The installer warns that `GEASS_ACME_EMAIL` is not set.**
The cluster installs and runs, but Geass creates no certificate issuer, so the cluster gets no HTTPS certificates. Set
the address on the running cluster. See [A certificate is never issued](#a-certificate-is-never-issued).

**The installer warns "No active host firewall detected; skipping port configuration."**
The installer found no active `ufw`, `firewalld`, or `iptables` rules, and opened nothing. That result is fine for K3s.
If you use a firewall of another kind, or a provider security group, open the ports yourself. The ports are TCP 6443,
10250, 80, and 443, and the overlay port. The installer opens these ports only in an active host firewall, never in the security
group of a provider. So open 80 and 443 in the security group for visitors and for certificates.

**The installer warns "The iptables rules that open the ports are not saved".**
The host filters with `iptables` but does not restore rules at boot. So the ports that the installer opened close again
after a reboot. Install `iptables-persistent` (Debian, Ubuntu) or `iptables-services` (Red Hat family), and save the
rules. See [Requirements](/docs/getting-started/requirements/#ports-and-firewall).

**The installer warns "Could not work out this machine's address".**
The installer found no usable address on the default route, so the `spec.serverURL` of the cluster is
`https://127.0.0.1:6443`. Geass joins workers on the address of the control plane node instead. So this problem does
not stop you from adding servers. To use another address, patch `spec.serverURL`. The loopback-address message in the
`GeassNode` section below shows how.

**The cluster is installed but a project's services never start, and `kubectl get geasscluster` shows it not ready.**
See [Everything says the cluster is not ready](#everything-says-the-cluster-is-not-ready).

## The dashboard and sign-in

**The dashboard does not load.**
Check the operator, and then your route to it.

```bash
kubectl -n geass-system get deploy geass-controller-manager
kubectl -n geass-system get svc geass-dashboard
```

Until you set a dashboard domain or use a Cloudflare tunnel, nothing exposes the service outside the cluster. To reach
the dashboard from your own computer, use a port forward, and open `http://localhost:8085`:

```bash
kubectl -n geass-system port-forward svc/geass-dashboard 8085:8085
```

After a domain is set, check DNS and the ingress. To check the ingress, run
`kubectl -n geass-system get ingress geass-dashboard`. Also see
[The dashboard domain stays pending](#the-dashboard-domain-stays-pending).

**Signing in says "dashboard authentication is unavailable".**
Geass could not load a login. Usually, no login exists. The installer creates one on a new install only. Create the Secret
`geass-dashboard-auth` in `geass-system` with a `users` key. Or set `GEASS_DASHBOARD_PASSWORD` on the operator. The
sample is `config/samples/geass_dashboard_auth.yaml`. Geass refuses the placeholder password `CHANGE_ME`, so replace
it. Geass hashes the real password at the first sign-in. See [Sign in to the dashboard](/docs/getting-started/sign-in/).

**Every sign-in says "invalid username or password".**
Geass locks sign-in in these cases:

- Five wrong attempts for one user from one address within a minute lock that user for that address for a minute.
- 25 wrong attempts for one user from all addresses together within a minute lock that user for everyone for a minute.
- If addresses cannot be told apart, five wrong attempts within a minute lock that user for everyone for a minute.
- Twenty failed sign-ins from one client address within a minute refuse that address for a minute.

While a lock is on, the message is the same. Wait a minute and try one time. If the sign-in still fails, read the users
in the Secret.

**Signing in works, then the dashboard asks for a sign-in again.**
You opened the dashboard over plain HTTP at an address other than `localhost`. Geass marks its session cookie `Secure`
everywhere except loopback, so the browser drops the cookie. Use HTTPS, or the port forward above. A session lasts 12
hours.

**"viewer role cannot access this".**
Your user has the viewer role. Viewers can read, but they cannot change anything, read logs, or open a terminal. Ask
for the admin role.

**The provider does not work, or nobody can sign in through it.**

1. Sign in with the password of an admin. On the sign-in page, click **Use an admin password instead**.
2. Open **Settings** → **Sign-in**.
3. Click **Switch back to passwords**.

If you cannot reach the dashboard at all, run this command to do the same:
`sudo k3s kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"auth":{"mode":"local"}}}'`.
Then see the messages below. While the provider decides, a user in the Secret who is not an admin cannot sign in with a
password. An admin can.

**The sign-in page says "Geass could not complete the sign-in with the identity provider".**
The operator log has the reason. To read it, run
`kubectl -n geass-system logs deployment/geass-controller-manager -c manager | grep -i "identity provider"`. These are
the usual reasons:

- The cluster cannot reach the provider. Or the provider is on this machine or at a link-local address
  (`the address is this machine or a link-local address`).
- The issuer that you saved is not exactly what the discovery document of the provider calls itself
  (`issuer did not match`). For Better Auth, the issuer includes `/api/auth`.
- The client secret is wrong (`invalid_client`).
- The redirect URI is not the one registered at the provider (`redirect_uri` mismatch). Use the address on the
  **Sign-in** page, with the scheme and host that the browser used.
- The token was signed with an algorithm that Geass does not accept. Geass accepts only a public-key algorithm.

**The sign-in page says "Your account has no role here" or "not one this installation lets in".**
The provider signed the person in, but Geass has no role for them. **Test sign-in** on the **Sign-in** page shows what
the token holds: the values of the role claim, and which required claim is missing. Do these checks:

- Make sure that the name of the claim is correct. A dotted path reaches into nested claims.
- Make sure that your **Roles** list has the names from the provider exactly as the token spells them.
- For Better Auth, make sure that the callback that adds `org_role` to the ID token runs for this person.

**The sign-in page says "Your account is not in an organization set up here".**
Organizations are on, and the token puts the person in no organization that Geass can use. **Test sign-in** lists the
organizations that the token named, and whether each one is set up. Make sure that these conditions are true:

- The name of the claim is correct.
- Each organization has a `GeassOrganization` whose `idpID` is the id of the provider.
- The organization is not suspended.
- A member who is not an administrator is in at least one workspace that the organization declares.

See [Organizations and workspaces](/docs/guides/organizations-and-workspaces/#if-something-goes-wrong).

**After you turn organizations on, projects are missing.**
Only the administrators of the platform can see a project that belongs to no organization. Assign the project
(`POST /api/projects/{project}/assign`), or sign in as a platform administrator. An admin who signs in with a password
is a platform administrator.

**An organization's administrator gets "only the platform's administrators can do this".**
These areas belong to the platform: settings, nodes, the domain, the registry, sign-in, alerts, shared servers, and
organizations. That is true whatever role someone holds in an organization. Ask a platform administrator, or name them
in `platformClaim`.

**The sign-in page says "The identity provider did not say what your email address is".**
The ID token has no `email` claim. Better Auth leaves the claim out of the ID token unless you add it in
`customIdTokenClaims`. See [Sign in with an identity provider](/docs/guides/external-sign-in/#better-auth).

**"Use ... to sign in" is disabled, or turning it on answers `400`.**
To turn it on, you need all of these:

- Complete settings.
- A test sign-in of exactly the saved settings in the last half hour, by someone who maps to admin. A test as a viewer
  does not count.
- An admin with a password in the Secret.

If you save the settings again, the test starts over. The message says which item is missing.

**"Switch to local sign-in before changing the identity provider".**
You cannot edit the settings while people sign in through the provider. To change them:

1. Click **Switch back to passwords**.
2. Change the settings.
3. Test the settings.
4. Turn on sign-in through the provider again.

**Someone who was removed at the provider can still use the dashboard.**
The session of that person lasts until it ends, an hour by default. The exception is when the provider sends a
back-channel logout to `/auth/oidc/backchannel-logout`. Most providers need that address to be public `https`. Until
then, to sign the person out, revoke what they made under **Settings** → **Tokens**, and lower **Session length**. When
**Also revoke the tokens and deploy hooks they made** is off, a logout that arrives ends their sessions but leaves their
tokens.

## Nodes

Workers are `GeassNode` objects. Check them first:

```bash
kubectl -n geass-system get geassnodes \
  -o custom-columns='NAME:.metadata.name,PHASE:.status.phase,MESSAGE:.status.message'
kubectl get nodes -o wide
```

A node moves through `Pending`, `Joining`, and `Ready`. If Geass creates the machine of a node at a cloud, the node has
a `Creating` step before `Joining`. A `Failed` node has the reason in its message. For nodes of a pool, and for
machines at a cloud, see [Pools and machines at a cloud](#pools-and-machines-at-a-cloud) below.

**A `GeassNode` is `Failed`.**

| Message | Cause and fix |
| --- | --- |
| "ssh key secret ref is required for worker nodes" | Set `spec.sshKeySecretRef` to a Secret that holds the private key of the worker. |
| "ssh key: …" or "no private key found in secret …" | The Secret is missing, or it holds no key under `ssh-privatekey`, `id_rsa`, `private-key`, or `key`. |
| "join token: …" | The Secret that the `spec.tokenSecretRef` of the cluster names (`geass-token`) is missing. Or that Secret holds no `token`, `value`, or `k3s-token` key. The **store worker join token** step of the installer creates the Secret. |
| "cluster not found: …" | `spec.clusterRef.name` names no `GeassCluster`. The installer creates one named `default`. |
| "could not determine the cluster's k3s version: …" | The operator could not list the control plane nodes. Check the log of the operator. |
| "ssh dial <address>: …" | The control plane cannot reach the SSH port on the worker (`spec.sshPort`, default 22). Or the user or key is wrong. |
| "ssh dial <address>: … the SSH host key of … is not the key recorded in known_hosts" (or "… is not listed in known_hosts") | The Secret has a `known_hosts` entry, and the machine at `spec.eip` does not match it. Another machine answers, the machine was rebuilt, or the entry is for a different address or port. Check the fingerprint on the machine, and then update `known_hosts` in the Secret. Without that entry, Geass does not check host keys. |
| `sudo: a password is required` in a step's output | A user other than `root` needs passwordless `sudo`. Geass runs each setup step through `sudo -n`. |
| "cannot reach the cluster API at <url> from this node: …" | The worker cannot reach the control plane on the address that Geass joins it on. The rest of the message says why. "the connection was refused" and "the connection timed out" mean that TCP 6443 is closed between the worker and the control plane. The block is in the host firewall or in the security group of the provider. "the host name does not resolve" and "something answered, but it is not a K3s API server" mean that `spec.serverURL` is wrong. The next entry describes "spec.serverURL on the GeassCluster is a loopback address". |

**The message says `spec.serverURL on the GeassCluster is a loopback address`.**
The `spec.serverURL` of the cluster is `https://127.0.0.1:6443` or similar. That address is the control plane itself,
and it means nothing to a worker. The installer now writes the address of the control plane there. When Geass finds a
loopback address, Geass joins workers on the address of the control plane node. So this message appears only when no
control plane node has an address. Set `serverURL` to an address that the worker can reach. Then delete and re-create
the `GeassNode`:

```bash
kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"serverURL":"https://10.0.0.5:6443"}}'
```

**A failed `GeassNode` does not try again after I fix the cause.**
If you edit a failed node, Geass does not provision it again. To ask for another attempt, add the retry annotation:
`kubectl -n geass-system annotate geassnode <name> geass.dev/retry=true`. Geass sets the node back to `Pending`, clears
its message, and removes the annotation. You can also delete the node and apply it again. When you delete the node,
Geass removes it from the cluster. If Geass can reach the machine over SSH on `spec.sshPort`, Geass also uninstalls K3s
from the machine.

**A node stays `Joining`.**
K3s installed, but the node did not register under its name. On the worker, run `journalctl -u k3s-agent -n 100`. Look
for connection errors to port 6443. Geass waits without a time limit.

**A node is `NotReady`, or the rail shows it as down.**
On the machine, check `systemctl status k3s` for the control plane, or `k3s-agent` for a worker. If a worker that Geass
joined stays not ready for five minutes, Geass provisions it again over SSH. For pressure states such as `memory high`
and `disk full`, see [Usage and capacity](/docs/guides/usage-and-capacity/).

## Pools and machines at a cloud

Start with the pool and its nodes. `geass pools show NAME` shows what the pool is doing, what it last failed at, and
what each node says. You can also use `kubectl`:

```bash
kubectl -n geass-system get geassnodepools
kubectl -n geass-system get geassnodepool burst -o jsonpath='{.status.phase}{"  "}{.status.message}{"\n"}'
kubectl -n geass-system get geassnodes -l geass.dev/pool=burst \
  -o custom-columns='NAME:.metadata.name,PHASE:.status.phase,MACHINE:.status.cloud.instanceID,MESSAGE:.status.message'
geass connections show hetzner
```

**A pool is `Paused`.** Three machines in a row failed to become nodes, so the pool stopped adding machines for 30
minutes. To continue:

1. Read `lastFailure` (`geass pools show NAME`).
2. Fix what it says, for example a firewall that blocks the control plane, a size that the account cannot use, or a
   missing permission.
3. Run `geass pools resume NAME`, or annotate the pool `geass.dev/resume=true`.

Each machine that fails costs at least the time that it lived. See
[Pause and resume](/docs/guides/node-pools/#pause-and-resume).

**A pool is `Degraded`.** The message of the pool says which of these problems it has:

- The settings are not valid, for example `maxNodes` below `minNodes`, or a missing size.
- The connection does not exist, is not for Compute, or belongs to a project.
- The cloud does not accept the credentials of the connection.
- The pool sets a setting that the cloud does not read, or it does not set a setting that the cloud needs. Azure and
  Oracle Cloud need `subnetId`.

Fix the problem, and the pool continues. See
[Cloud connections for machines](/docs/guides/connectors/clouds/#troubleshooting).

**A connection is not accepted.** `geass connections show NAME` shows the message from the cloud. Check the
credentials against the section for that cloud in [Cloud connections for machines](/docs/guides/connectors/clouds/).
Then ask again with `geass connections verify NAME`. Credentials expire. The client secret of Azure and the token of
UpCloud expire, and Geass cannot count the days.

**A node stays `Creating`.** Read its message.

| Message | Cause and fix |
| --- | --- |
| "waiting for the provider to start the machine (state ...)" | The cloud is slow, out of room, or at a limit. If a machine is not running with an address 10 minutes after the request, the machine is deleted and the node fails. |
| A message from the cloud with the problem `Capacity` or `Quota` | The cloud has no room for this size in this place, or the account is at a limit. The node waits: 2 minutes between tries for capacity, and 5 minutes for a limit. After 3 minutes, the pool tries its fallback size and region. Add fallbacks, ask the cloud to raise the limit, or delete machines that you do not use. |
| "waiting for the machine to accept SSH while it finishes its first boot" | The machine is up, and port 22 does not answer yet. The operator waits for up to 18 minutes after the machine was requested. If the wait continues, the firewall does not let the operator in. The firewall that Geass makes allows only the addresses that Kubernetes lists for the nodes of the control plane. Check `kubectl get nodes -o wide`. Use `addressType: Private` in the same network, or a multi-site cluster. |
| "the provider refused the credentials: ..." | The node fails. The reason from the cloud follows. The reason can be a wrong key, a policy of the key, or a policy of the organization of the account that denies the launch. At Amazon, that policy is a service control policy, which the message names. Only the owner of the organization can change it. Fix the cause and ask for another attempt. Three such failures in a row pause the pool. |
| "opening the control plane's firewall for the machine: ..." | `spec.network.controlPlaneFirewall` is wrong: the connection, its permission on that firewall, or the target ID. Geass can change only an AWS security group and a Hetzner firewall. See [Let Geass open the control plane](/docs/guides/node-pools/#let-geass-open-the-control-plane). |
| "the provider no longer has the machine it accepted ..." | The cloud refused the machine after it accepted the request (no room, or a limit). Or someone deleted the machine. The node fails, and the pool makes another. |
| "the node's name is longer than 55 characters ..." | The name of a single server is too long. The names in a pool are shorter, because the name of a pool is at most 48 characters. |

**A node stays `Joining`, then fails after 15 minutes.** K3s was installed, and the node never registered. The machine
cannot reach 6443/tcp on the control plane, or UDP 51820 in a multi-site cluster, or 8472/udp in a default cluster.
Open the port at the firewall of the control plane, or set `controlPlaneFirewall`. When the node fails, the machine is
deleted, so its log is gone. Check the ports first.

**Machines are left at the cloud that no node owns.** These machines are orphans. A pool reports them in
`status.orphans`, on its page, and in its message. The pool deletes them only with `orphanPolicy: Delete`, and only
after 20 minutes. Delete them in the console of the cloud. Or, after you check the list, change the policy. See
[Machines no node owns](/docs/guides/node-pools/#machines-no-node-owns).

**A pool does not add a node although pods are waiting.** Go through the list in
[Node pools](/docs/guides/node-pools/#troubleshooting):

- Autoscaling is on, and the pool is not suspended or paused.
- The pods could not start for the delay.
- The region and tags of the pool match those of the pods.
- The pods are small enough for one node.
- The pool is below `maxNodes`.

**A pool does not remove an idle node.** [What keeps a node](/docs/guides/node-pools/#what-keeps-a-node) lists the
reasons:

- A pod with no controller.
- A volume claim, a host path, or an on-disk `emptyDir`.
- A node younger than `minNodeLifetimeMinutes`.
- A K3s upgrade.
- The annotation `geass.dev/scale-down-disabled`.

`geass pools show NAME` and the operator log name the reason.

**A node does not go away when I delete it.** When you delete a node whose machine Geass created, Geass waits until the
cloud has deleted the machine. If the connection is gone permanently, annotate the node
`geass.dev/abandon-instance=true`. Geass then lets the node go, and leaves the machine at the cloud for you to delete.

**The services restarted when I made my first pool.** This restart is expected, one time. See
[What runs on a pool's nodes](/docs/guides/node-pools/#what-runs-on-a-pools-nodes).

## Services, databases and buckets that are stuck

### A service stays "Setting up"

The condition is "Service is starting" and does not change. Geass waits until every replica is available, so a service
that is only slow stays in this state. A copy that cannot start does not wait forever. Geass reports "The service cannot
start: `<container>` is `<reason>`" and counts it as a failure in these cases:

- A pod is in `CrashLoopBackOff`, `ImagePullBackOff`, `ErrImagePull`, `InvalidImageName`,
  `CreateContainerConfigError`, or `CreateContainerError`.
- The Deployment did not become ready within its progress deadline.

There is one exception: a copy that waits for the connection Secret of a database or bucket that is still starting.
That copy shows "Waiting for database `<name>`" (or "bucket"), and Geass does not count it as a failure. The label
changes to **Unavailable**. After five failures in a row, about two minutes, the service is **Failed**. The message
stays in the record of the deploy under **Deployments**.

```bash
kubectl -n morning-beach-production get pods -o wide
kubectl -n morning-beach-production describe pod <pod>
kubectl -n morning-beach-production logs <pod> --previous
```

| What `describe` shows | Cause and fix |
| --- | --- |
| `ImagePullBackOff` or `ErrImagePull` | The image name or tag is wrong, or the registry needs credentials. Check the **Source** of the service. |
| `CrashLoopBackOff` | The container starts and exits. Read its logs. Check the port: the service listens on **Port** (default 8080). |
| Readiness probe failed | The health check path returns an error. Check the **Healthcheck path** in **Settings**. |
| `Insufficient cpu` or `Insufficient memory` | The nodes are full. See [A resource says there is not enough capacity](#a-resource-says-there-is-not-enough-capacity). |
| `didn't match Pod's node affinity/selector` | No node has the region of the project, the tags that you selected, or the tags that the project requires. See [Scaling and placement](/docs/guides/scaling-and-placement/) and [Compliance node tags](/docs/guides/compliance-tags/). |

### A service that sleeps when idle does not sleep, or does not wake

Read its `ScaleToZero` condition, and see [Sleep when idle](/docs/guides/scale-to-zero/#troubleshooting). If a request
wakes the service and the service cannot start, the service shows "The service was woken but its copy cannot start: …".
Or it shows "The service was woken but has not started within 10m0s".

### A resource is "Unavailable" and names a project or cluster

Geass checks the project before it creates anything in it. The message is one of these:

- `project "<name>" does not exist`
- `project "<name>" is not ready`
- `project "<name>" cluster "<cluster>" is unavailable: …`
- `project "<name>" cluster "<cluster>" is not ready`
- `project "<name>" has no <environment> environment`

Before you create resources through manifests, create the project and wait until it is ready. If the project or cluster
was not ready for a short time, these messages count as failures, and the resource might be **Failed**. When the project
is ready, click **Retry**.

### Everything says the cluster is not ready

You see one or more of these signs:

- The condition of a project reads "Referenced GeassCluster is not ready".
- The resources in the project say `project "<name>" cluster "<cluster>" is not ready`.
- The `Ready` condition of the cluster itself is false with the reason `ClusterPending`.

```bash
kubectl -n geass-system get geasscluster default -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}{"\n"}{end}'
kubectl -n kube-system get helmcharts
kubectl -n cert-manager get pods
kubectl -n monitoring get pods
kubectl -n longhorn-system get pods -o wide
```

`AddonsReady` is false while an add-on is installing or unhealthy. The add-ons are cert-manager, monitoring, and, when
it is on, Longhorn. Longhorn counts as ready only when every node that can run workloads has a ready Longhorn manager.
So a node that joined recently, or a node without the iSCSI initiator, holds back the whole cluster until its manager
is ready. Fix the add-on or the node, and the cluster becomes ready by itself. To see why a chart is not installing,
read its job: `kubectl -n kube-system get jobs` and `kubectl -n kube-system logs job/helm-install-<chart>`.

### A resource says there is not enough capacity

The message begins "This service needs about …" or "There is no ready node in …". Open **Usage** to see how full the
nodes are. Then add a node, lower the size, or free something. [Scaling and placement](/docs/guides/scaling-and-placement/)
gives the messages and fixes. If the resource is already **Failed**, click **Retry** after the capacity exists. A
[node pool](/docs/guides/node-pools/) can add the room itself. Geass lets a service in when an autoscaling pool could
make room, and refuses the service when the pool is at its maximum.

### A MySQL or Redis database says "Could not pull the database image …"

The pod of the database server cannot get its image from Docker Hub. The message names the image and the state:

- `ErrImagePull`: a pull failed a moment ago.
- `ImagePullBackOff`: the kubelet waits to try again.
- `InvalidImageName`.

A pull that is only slow shows "Service is starting", not this message. The database is **Unavailable** while the
kubelet continues to try. The database is **Failed** when the image has not been pulled for 10 minutes. To fix the problem:

1. Read the error from the registry in the events of the pod.
2. Fix the route from the node to Docker Hub. Or load the image onto the nodes, or use a mirror.
3. Click **Retry**.

See [Databases](/docs/guides/databases/#troubleshooting).

```bash
kubectl -n payments-dev describe pod geass-redis-db-orders-master-0
```

### A database says "High availability requires a cluster with at least three healthy nodes"

You need three ready nodes that are not cordoned, and a readiness report that says so. See
[High availability](/docs/operations/high-availability/). That page also explains why **Run readiness check** might seem
to do nothing.

### A database sits at "Storage is starting", then "Storage is unavailable"

A new database waits for Longhorn to create its storage. If Longhorn is not ready for 15 minutes, the database reports
"Storage is unavailable". See [Volumes do not attach](#volumes-do-not-attach).

### The object storage server sits at "Storage is starting", then "Storage is unavailable"

The server waits for Longhorn to create its volume, like a new database. If Longhorn is not ready for 15 minutes, the
server reports "Storage is unavailable". A server placed in a region or on tags can also wait without a limit for the
storage class of that region or those tags. See [Volumes do not attach](#volumes-do-not-attach) and
[Object storage](/docs/guides/object-storage/#troubleshooting).

### A database stays "Deleting"

When you delete a database that has backups, Geass first takes one last backup, so that you can restore the data for
seven days. Geass waits for that backup for at most 10 minutes. Then Geass removes the database, even if the backup has
not finished. If the database is still there after that time, find what holds it:

```bash
kubectl -n geass-system get geassdatabase orders -o jsonpath='{.metadata.finalizers}{"\n"}'
kubectl -n morning-beach-production get cluster.postgresql.cnpg.io,backups.postgresql.cnpg.io
```

## Changes that will not deploy

**I changed a setting and the top bar still says "Up to date".**
Some settings apply when you save them, and they never count as pending:

- Replicas
- Autoscaling
- **Sleep when idle**
- The domain and its HTTPS and DNS verification switches
- **Enable metrics**
- Backup settings
- The watch paths of a repository

Settings marked **Applies instantly** are these settings. Everything else waits for **Deploy changes**. That includes
source, port, variables, command, **Resources**, and **Run on**.

**There is no Deploy changes button.**
Viewers do not see the button. The button is also disabled when nothing is pending.

**When I click Deploy changes, it shows "Nothing was deployed".**
A deploy applies to the environment that is selected in the environment bar. Select the environment that holds the
pending change.

**A deploy is refused with "This service needs about … Scale up the cluster before creating it".**
The capacity check failed. The dashboard counts the whole workload against free room, even on a redeploy. See
[Scaling and placement](/docs/guides/scaling-and-placement/).

**The service says "Service has pending changes and no last deployed snapshot".**
The service has pending-change markers but no record of what was last deployed. This problem happens to a service that
someone edited by hand. Deploy the service one time from the dashboard.

**The service is Failed after I deployed.**
Fix the cause and click **Retry**. Or change a setting and deploy again. See
[How Geass gives up](#how-geass-gives-up-and-how-to-start-again).

## Builds

A service from a Git repository builds before it runs. Each build is a `GeassBuild` in `geass-system`, named
`<service>-build-<hash>`. The build runs a job named `<build>-kaniko` with two steps:

- An init container `git` that clones the repository.
- A `kaniko` container that builds the Dockerfile and pushes the image.

The job has up to three attempts.

```bash
kubectl -n geass-system get geassbuilds
kubectl -n geass-system get geassbuild <build> -o jsonpath='{.status.phase}{"  "}{.status.failureReason}{"\n"}'
kubectl -n geass-system logs job/<build>-kaniko -c git
kubectl -n geass-system logs job/<build>-kaniko -c kaniko
```

In the dashboard, open the **Deployments** tab of the service, open the failed deployment, and read **Build Logs**.

An hour after a build finishes, its job is removed, and with the job its pod and log. The result stays on the
`GeassBuild`. The log is archived when the service asks for that. So read `kubectl logs` soon, or use **Build Logs**.
Geass keeps the 10 newest finished builds of a service and deletes older ones. Geass never deletes the build that the
service runs. A finished build does not run again, because its job is gone. To start the build over, push to the
branch, or use `POST /api/apps/<service>/build`.

| Symptom | Cause and fix |
| --- | --- |
| The service stays not ready with the condition "Git source is waiting for a successful build" | The build is running or queued. Check `status.phase`: `Pending`, `WaitingForCI`, `Running`, `Succeeded`, `Failed`, or `Cancelled`. |
| `status.phase` is `WaitingForCI` | **Wait for CI** is on, and the checks of the commit have not succeeded. Wait for the checks, or turn off **Wait for CI** in **Source**. |
| `failureReason` "GitHub API returned <status>" or "GitHub did not return a commit for branch …" | Before it builds, Geass gets the commit for the branch from GitHub. When **Wait for CI** is on, Geass also reads the checks of the commit. The branch name, the token of the GitHub connection, or its access to the repository is wrong. See [GitHub and builds](/docs/guides/github-and-builds/). |
| The `git` container fails | The branch or repository name is wrong, or the connection cannot read a private repository. |
| `failureReason` "kaniko Job failed" | The build itself failed. Read the `kaniko` log. Look for a Dockerfile error, a wrong **Dockerfile path** or root directory, or a failed push. |
| `failureReason` "Kaniko completed without an immutable image digest" | The build finished, but Geass found no digest. The termination message of the `kaniko` container was empty, and its log has no `Pushed <repository>@sha256:…` line. Read the `kaniko` log for a push error. A build that failed this way before Geass read the termination message did push its image. Run it again with a push or `POST /api/apps/<service>/build`. |
| `failureReason` "no container registry is set for builds" | Neither the service nor the platform names a registry. See below. |
| No `geass/build/<service>` status, GitHub Deployment or pull request comment on GitHub | The build and the deploy are fine. Geass reports back to GitHub on the side, and never holds up the build or the deploy for the report. Look at the GitHub connection of the project: `kubectl -n geass-system get geassgithubconnection <project>-github -o yaml`. A `GitHubReporting` condition that is `False` with the reason `PermissionMissing` means that the GitHub App lacks permissions. The app was registered before Geass reported to GitHub, and it lacks **Commit statuses**, **Deployments**, or **Pull requests** at read and write. **Test GitHub App** in **Settings** → **Connectors** lists them. See [Give an existing GitHub App the new permissions](/docs/guides/github-and-builds/#give-an-existing-github-app-the-new-permissions). Also make sure that **Report to GitHub** is on in **GitHub** for the project. |

:::note
Geass builds push to a registry that you configure. Geass has no registry of its own. Without a registry, the service
does not build and shows `no container registry is set for builds`. The destination comes from
`spec.build.registry.repository` on the service. Or, for every service, it comes from `spec.registry.repository` on the
`platform` object. An admin sets the platform registry with the REST API. The dashboard has no form for the registry
yet. The API also stores the login as a Docker registry Secret:

```bash
curl --fail -X POST https://geass.example.com/api/settings/registry/save \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d repository=ghcr.io/example/builds -d username=builder --data-urlencode password="$REGISTRY_TOKEN"
```

Or use `kubectl` with a Secret of type `kubernetes.io/dockerconfigjson` in `geass-system`, named in `credentialRef`:

```bash
kubectl -n geass-system patch geassplatformconfig platform --type merge \
  -p '{"spec":{"registry":{"repository":"ghcr.io/example/storefront","credentialRef":{"name":"registry-credentials"}}}}'
```

The `platform` object exists after you save a dashboard domain, or connect GitHub or Cloudflare. Before that, the patch
fails. Geass passes the repository value to Kaniko as the image to push. So if different services need different
images, set the value for each service. The nodes pull the built image with a copy of the same login. That copy is the
Secret `<service>-registry-pull` in the environment namespace.
:::

## Domains and certificates

### The dashboard domain stays pending

To see the domain that you saved, open **Settings**, and then select **Domain**. That page does not show the
verification status. Read the status from the `platform` object:

```bash
kubectl -n geass-system get geassplatformconfig platform -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}: {.message}{"\n"}{end}'
```

| Reason | Message | Fix |
| --- | --- | --- |
| `NoDomain` | "Dashboard domain is not configured" | Set a root domain. |
| `PendingDNS` | "DNS not detected yet" | The host of the dashboard does not resolve to the public address of the server. Add the DNS record and wait. When DNS is correct, `dig +short geass.example.com` prints the public IP of the server. |
| `PendingDNS` | "could not determine this server's public IP" | Geass found no public address on the nodes, and it could not look one up. Set the record by hand, or check the outbound access of the server. |
| `PendingDNS` | "Could not create dashboard DNS record in Cloudflare" | The Cloudflare connection cannot write the record. Check its token under **Settings**, and then **Connectors**. |
| `PendingHTTPS` | "Dashboard HTTPS endpoint is not reachable yet" | DNS is correct, but HTTPS does not answer. When the certificate issuer of the cluster is the problem, the message ends with the reason in parentheses. For example: "(No ACME contact email is configured, so the letsencrypt-prod ClusterIssuer was not created …)". See [A certificate is never issued](#a-certificate-is-never-issued). |
| `PendingTunnel` | "Cloudflare tunnel ID is not configured" | Complete the Cloudflare tunnel settings. |
| `Error` | "Cloudflare credentials are unavailable" or "… are incomplete" | Connect Cloudflare again under **Settings**, and then **Connectors**. |
| `ZoneRequired` | "Select a Cloudflare zone in domain settings (multiple zones match this token)" | Select the zone. |
| `VerificationTimedOut` | "Not verified after 15 minutes: …" | The cause follows the colon. Geass continues to check every five minutes. So when you fix the cause, the domain still turns green. |

### A service's HTTPS domain does not work

1. Check DNS. The host must resolve to a node that serves ports 80 and 443. Run `dig +short app.example.com`.
2. Check the ports. The installer opens 80 and 443 only in an active host firewall on the control plane. The installer
   never opens them in the security group of the provider, and never on workers. Open the ports on every node that
   serves web traffic, and in the security group of the provider.
3. Make sure that the cluster has a certificate issuer:
   `kubectl -n geass-system get geasscluster default -o jsonpath='{.status.conditions[?(@.type=="ClusterIssuerReady")].message}'`.
   See [A certificate is never issued](#a-certificate-is-never-issued).
4. Check the ingress and certificate:

   ```bash
   kubectl -n morning-beach-production get ingress,certificate
   kubectl -n morning-beach-production describe certificate <service>-tls
   ```

### A certificate is never issued

Every HTTPS ingress, including the ingress of the dashboard, asks cert-manager for its certificate from a ClusterIssuer
named `letsencrypt-prod`. Geass creates that issuer itself, but only after Geass has an email address for the Let's
Encrypt account.

:::caution
Give the installer an ACME email address as `GEASS_ACME_EMAIL`. Without that address, Geass creates no issuer, and no
HTTPS certificate is issued. The cluster still installs and runs. But the dashboard domain stays on `PendingHTTPS`, and
the HTTPS of every service stays unavailable. Let's Encrypt refuses placeholder domains such as `example.com`.
:::

**What Geass creates, and when.** The operator creates the `letsencrypt-prod` ClusterIssuer when cert-manager is ready
and `spec.addons.certManager.acmeEmail` is set on the cluster. The installer writes that field from
`GEASS_ACME_EMAIL`. The issuer has these properties:

- It is an ACME issuer for the production server of Let's Encrypt
  (`https://acme-v02.api.letsencrypt.org/directory`).
- It keeps the account key in the Secret `letsencrypt-prod-account-key`.
- It solves the HTTP-01 challenge through the `traefik` ingress class.

If you delete or edit the issuer, the operator puts it back. When you change the address, the operator updates the
issuer. While cert-manager is installing or the account is registering, the operator looks again every 30 seconds.

Geass does not change these things:

- Geass never overwrites a `letsencrypt-prod` issuer that you created yourself. That includes an issuer that you made
  by hand before Geass created its own. Geass reports that issuer as `ExternallyManaged`.
- If you remove the address later, Geass does not delete the issuer that it made. Certificates issued through that
  issuer still renew through it.
- If you turn off cert-manager (`spec.addons.certManager.enabled: false`), Geass manages no issuer at all, and it
  removes the cert-manager that it installed. You run cert-manager and the issuer yourself.
- A dashboard published through a Cloudflare tunnel gets its certificate from Cloudflare, so the issuer is not used for
  that dashboard.

To set or change the address on a running cluster, including a cluster installed without the address, run this
command:

```bash
kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"addons":{"certManager":{"acmeEmail":"you@example.com"}}}}'
```

Use a real address in place of `you@example.com`. Geass creates or updates the issuer within seconds.

**Check what Geass says.** The cluster has a `ClusterIssuerReady` condition. This condition is separate from the
`Ready` condition of the cluster. So a cluster can be ready with no working issuer.

```bash
kubectl -n geass-system get geasscluster default \
  -o jsonpath='{.status.conditions[?(@.type=="ClusterIssuerReady")]}{"\n"}'
kubectl get clusterissuer letsencrypt-prod
kubectl describe clusterissuer letsencrypt-prod
```

| Reason | Meaning | Fix |
| --- | --- | --- |
| `ACMEEmailMissing` | No address is set, so Geass did not create the issuer. | Patch the cluster as shown above. |
| `WaitingForCertManager` | cert-manager is not ready, or its API is not served yet. | Wait. See [Everything says the cluster is not ready](#everything-says-the-cluster-is-not-ready). |
| `IssuerRegistering` | The issuer exists, and cert-manager has not finished the registration of the ACME account. | Wait a minute. |
| `IssuerNotReady` | cert-manager reports that the issuer is not ready, and the message ends with its reason. This also applies to an issuer that you created yourself. The most likely reason is an address that Let's Encrypt refuses, or no route from the cluster to Let's Encrypt. | Set a real address, and make sure that the server can reach the internet. |
| `IssuerApplyFailed` | The operator could not create or update the issuer. The operator tries again every 30 seconds. The error is in the log of the operator, not in the message. | `kubectl -n geass-system logs deploy/geass-controller-manager -c manager --tail=100`. |
| `IssuerAccessDenied` | The cluster role of the operator does not have permission on `clusterissuers.cert-manager.io`. | Apply the current manifests of the operator. They grant get, list, watch, create, update, and patch. |
| `ExternallyManaged` | A `letsencrypt-prod` issuer exists that Geass did not create. Geass does not change it. | The settings of that issuer decide whether certificates work. To let Geass manage the issuer, delete your issuer. |
| `ManagedByAnotherCluster` | Another `GeassCluster` owns the issuer, which is cluster-wide. Geass does not change it. | Change the address on the cluster that owns the issuer. |
| `IssuerReady` | The issuer works. | If certificates still fail, see the next list. |

While HTTPS is not reachable, the same message is appended to the `PendingHTTPS` status of the dashboard domain.

**If the issuer is ready and certificates still fail.** Check these items in this order:

1. **DNS.** The host must resolve to the public address of a node that serves ports 80 and 443. Let's Encrypt connects
   to that address to validate the domain.
2. **Ports 80 and 443.** Port 80 carries the HTTP-01 validation, and port 443 carries visitors. The installer opens
   both ports in an active host firewall on the control plane. The installer does not open them in the security group
   of the provider, or on workers. Open the ports there.
3. **The address.** If the address is at a domain that Let's Encrypt refuses, the account cannot register. The issuer
   then shows `IssuerNotReady`.
4. **Rate limits.** Let's Encrypt limits how often it issues certificates, and how many times a validation can fail.
   After repeated failures, the order or certificate reports a rate-limit error. Fix the cause, and then wait before
   you try again.

To see where a certificate is stuck, follow the chain. For each ingress, cert-manager creates a Certificate named after
the TLS Secret. That name is `<service>-tls`, or `geass-dashboard-tls` in `geass-system` for the dashboard. The
Certificate makes a CertificateRequest, an Order, and one Challenge for each host. These objects are in the namespace
of the ingress:

```bash
kubectl get certificate -A
kubectl -n morning-beach-production describe certificate web-tls
kubectl get certificaterequests,orders.acme.cert-manager.io,challenges.acme.cert-manager.io -A
kubectl describe challenge -A
```

The `State` and `Reason` of a challenge name the failure. For example, a self-check cannot fetch the challenge URL
because of DNS, or because port 80 is blocked. While a challenge runs, cert-manager serves it from a short-lived Pod,
Service, and Ingress named `cm-acme-http-solver-…` in the same namespace. cert-manager itself must be running:
`kubectl -n cert-manager get pods`.

## Backups

A database with backups on shows one of four states on its **Backups** tab:

| State | Message | Cause and fix |
| --- | --- | --- |
| **Needs storage** | "Choose where backups are stored to protect this database" | The project has no backup storage. Open **Settings** for the project, then **Backups**, and select an AWS connection. |
| **Needs storage** | "Backup storage is not working: …" | The bucket or its credentials fail. Read the reason after the colon, and check the connection under **Settings**, and then **Connectors**. |
| **Starting** | "Taking the first backup" | The first backup has not finished. Wait. |
| **At risk** | "Recent changes are not being saved to backup storage" | Postgres is not archiving its log to the bucket. Check storage and connectivity. |
| **At risk** | "The latest backup failed" | The last scheduled backup failed. Check storage, and then wait for the next backup. |
| **Protected** | "Backups are running" | Healthy. |

```bash
kubectl -n geass-system get geassdatabase orders -o jsonpath='{.status.backup}{"\n"}'
kubectl -n geass-system get geassbackuparchives
kubectl -n morning-beach-production get backups.postgresql.cnpg.io,scheduledbackups.postgresql.cnpg.io   # Postgres
kubectl -n morning-beach-production get cronjob,job -l geass.dev/backup-for=orders                       # MySQL and SQLite
```

Postgres backs up continuously. MySQL and SQLite take a copy daily, between 02:00 and 02:59 UTC, at a minute that
depends on the database. See [Backups and restore](/docs/guides/backups-and-restore/).

## Copies of the cluster do not arrive

This section applies only to a cluster that is set to keep copies of its own state in a bucket. See
[Disaster recovery](/docs/operations/disaster-recovery/). **Cluster backup** on the Usage page shows what the operator
found. A copy that stopped arriving is a **Cluster problem** alert. To read the same information with `kubectl`, and to
see what K3s itself says, run these commands:

```bash
kubectl -n geass-system get geasscluster default -o jsonpath='{.status.conditions[?(@.type=="EtcdSnapshotsCurrent")].message}{"\n"}'
sudo k3s etcd-snapshot ls
sudo journalctl -u k3s | grep -i -E "s3|snapshot"
```

| Message | Cause and fix |
| --- | --- |
| "No copy has reached the bucket in 13 hours" | K3s on the control plane was never set to send copies. Run the installer there with `etcd-snapshots enable`. A cluster with three control plane servers needs it on each server |
| "The access key for the bucket is not in the Secret geass-system/geass-etcd-snapshot-credentials" | The key was removed or never stored. Set the key again under **Cluster backup** |
| "The bucket could not be read with this access key" | The key does not have a permission, or the name or region of the bucket is wrong. The key needs the policy on the [Disaster recovery](/docs/operations/disaster-recovery/#1-make-the-bucket-and-a-key-for-it) page |
| "The newest copy in the bucket is … old" | K3s sent copies and then stopped. `journalctl -u k3s` says why. Usually the key or the bucket changed |
| `Unable to initialize S3 client … etcd-s3-config-secret … not found` in the K3s log | K3s is set to send copies, but the cluster has not told K3s where. Set the bucket, or run `etcd-snapshots disable` on that server |

## Volumes do not attach

Database storage and the storage of the object storage server use Longhorn. Longhorn needs the iSCSI initiator
(`iscsid`) on every node. These are signs of trouble:

- A database or the object storage server at "Storage is starting" or "Storage is unavailable".
- A pod `Pending` with a volume error.
- The cluster not ready, with `AddonsReady` false.

```bash
kubectl get storageclass
kubectl -n morning-beach-production get pvc
kubectl -n morning-beach-production describe pvc <claim>
kubectl -n longhorn-system get pods -o wide
```

| Finding | Cause and fix |
| --- | --- |
| No `geass-replicated` class | Longhorn is not ready. Geass creates the classes only after the Longhorn driver is registered and every node has a ready manager. |
| A `longhorn-manager` pod not ready on one node | That node does not have the iSCSI initiator. The installer and the worker setup try to install it, and they continue if they cannot. On Debian and Ubuntu, run `sudo apt-get install -y open-iscsi && sudo systemctl enable --now iscsid`. On other systems, install `iscsi-initiator-utils` (Red Hat family) or `open-iscsi` (SUSE), and then enable `iscsid`. |
| The installer logged "Could not set up the iSCSI initiator, so database storage will not work on this machine until it is installed" | The same cause, on the control plane. Look in the installation log. |
| A database in a region waits for a regional class | Every node in the region needs a ready manager. See [Scaling and placement](/docs/guides/scaling-and-placement/). |
| A database of a project that requires node tags waits for its class | Every node that has the tags needs a ready manager. Then Geass creates `geass-tagged-<hash>`. See [Compliance node tags](/docs/guides/compliance-tags/). |

For more information, see [Storage](/docs/guides/storage/).

## Related

- [Usage and capacity](/docs/guides/usage-and-capacity/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Sleep when idle](/docs/guides/scale-to-zero/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [High availability](/docs/operations/high-availability/)
- [Node pools and autoscaling](/docs/guides/node-pools/)
- [Cloud connections for machines](/docs/guides/connectors/clouds/)
- [Nodes at more than one provider](/docs/operations/multi-provider/)
- [Logs, metrics and terminal](/docs/guides/observability/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [Upgrading](/docs/operations/upgrading/)
