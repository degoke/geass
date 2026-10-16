---
title: "Troubleshooting"
description: "Symptom-driven fixes for installs, sign-in, nodes, stuck services and databases, deploys, builds, domains, certificates, backups, volumes and full clusters."
---

This page is organised by what you see. Each entry says where to look in the dashboard and with `kubectl`, what usually causes it, and how to fix it. Messages in quotation marks are the exact text of the dashboard or of a condition, as each entry says. The dashboard rewrites many condition messages into plain language, so the exact text is often in `kubectl` only.

## First, see what is wrong

### Where things live

| What | Where |
| --- | --- |
| The operator and dashboard | Deployment `geass-controller-manager` in namespace `geass-system`. The dashboard is part of the operator and listens on port 8085 behind the service `geass-dashboard`. |
| Geass resources (`GeassApp`, `GeassDatabase`, `GeassProject`, `GeassCluster`, `GeassNode`, `GeassBuild`, and others) | Namespace `geass-system` |
| A project environment's pods, services and ingresses | Namespace `<project>-<environment>`, for example `morning-beach-production` |
| Add-ons | `cert-manager`, `monitoring`, `longhorn-system`, `cnpg-system`, `geass-logging` and, when switched on, `keda` |
| Helm installs of add-ons | `HelmChart` objects in `kube-system`, installed by `helm-install-<name>` jobs |

Run the `kubectl` commands on the control plane as `sudo k3s kubectl ...`, or set `KUBECONFIG=/etc/rancher/k3s/k3s.yaml`. This page writes `kubectl`.

```bash
kubectl -n geass-system get pods
kubectl -n geass-system logs deploy/geass-controller-manager -c manager --tail=200
kubectl -n geass-system get geasscluster default -o jsonpath='{.status.conditions}{"\n"}'
```

### What the status labels mean

A service, database or bucket shows one of these labels. The dashboard works the label out from the resource's `Ready` condition (its reason and its message).

| Label | What it means | What to do |
| --- | --- | --- |
| **Draft** | It was created in the dashboard or through its API and not deployed yet. The condition reads "Service is configured and waiting for deployment" (or "Resource is configured…"). A service you applied from a manifest without `deploy.enabled: true` carries no draft marker, so the dashboard shows it as **Unavailable** instead. | Choose **Deploy changes**. |
| **Setting up** | A new resource that has no condition yet, and the few messages that count as progress: "Service is starting", "Postgres database is starting" (any "… database is starting"), and "Installing … chart". | Wait. If it lasts, see [A service stays "Setting up"](#a-service-stays-setting-up). |
| **Unavailable** | The catch-all for every other not-ready condition that is not a failure. It includes real shortages (capacity, object storage, an AWS connection, a project that is not ready) and also plain waiting: "Storage is starting", "Git source is waiting for a successful build", "Restoring database from backup", "High availability requires …". The label does not tell the two apart. | Read the condition with `kubectl` (see below). Fix a shortage; for a waiting message, wait. |
| **Failed** | Geass gave up. The reason is `ProvisionFailed` ("Provisioning failed after repeated errors. Update settings or platform configuration, then save or redeploy to try again."), or `RolloutFailed`, which a service reaches only when `spec.deploy.failureThreshold` is set in a manifest; the dashboard never sets it. | For `ProvisionFailed`, fix the cause, then choose **Retry**. See below. |
| **Deleting** | The resource is being removed. | Wait. See [A database stays "Deleting"](#a-database-stays-deleting). |
| **Ready** | It is running. | None. |

The line under the label is plain language too, for example "This database isn't available yet. Check Platform settings or your configuration." The condition's own text is what the entries on this page quote. Read it with:

```bash
kubectl -n geass-system get geassapp web \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}{"\n"}'
```

### How Geass gives up, and how to start again

While a resource is not ready, Geass tries again every 30 seconds and records the reason in the `Ready` condition. Most reasons count as failures. A resource becomes **Failed** after five failed passes in a row, or after three minutes of failures, or at once for an error that cannot heal itself (an `access denied`, an unsupported engine). Waiting states never count: a draft, pending changes, a build in progress, an install in progress, "is starting", a restore, "High availability requires …", and a service that sleeps when idle and is starting after a request.

A failed resource stays paused until its settings change or you choose **Retry** on its page (Geass shows "Retrying provisioning"). **Retry** appears only on a resource whose reason is `ProvisionFailed`. Fix the cause first, or it fails again in about two minutes.

The dashboard's **Deploy history** and the final condition hide the raw error, with one exception: a message that says a service "cannot start" is kept, because it says what is wrong with the service itself (see [A service stays "Setting up"](#a-service-stays-setting-up)). To read the real cause, choose **Retry** and read the condition during the next minutes, before Geass gives up again:

```bash
kubectl -n geass-system get geassapp web \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")]}{"\n"}'
kubectl -n geass-system get geassapp web -w \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}{"\n"}'
```

Use `geassdatabase`, `geassobjectstore` or `geasslogicaldatabase` for the other kinds. Open the service's **Deployments** tab, and a deployment inside it, for **Details**, **Build Logs** and **Deploy Logs**.

## Install

**The install script prints "Could not download Geass from …".**
The download failed. The line above the message is curl's own error, for example `curl: (22) The requested URL returned error: 404` for a version or file that does not exist, or `curl: (6) Could not resolve host` for no route to GitHub. Check that a `GEASS_VERSION` you pinned is a release and has a file for your architecture, and that the server can reach `github.com` over HTTPS or through your proxy. The script exits with "unsupported architecture", before it downloads anything, on anything but x86-64 and arm64. It deletes its temporary file when it ends.

**"unsupported architecture", "insufficient memory: … (a server with 2 GB is needed …)" or "insufficient disk space on /: … (10240 MB required)".**
The **system checks** step stops at the first requirement the machine does not meet: Linux, amd64 or arm64, a server with 2 GB of memory (at least 1700 MiB of `MemTotal`, which a machine sold as 2 GB reports) and 10 GB free on `/`. See [Requirements](/docs/getting-started/requirements/).

**An install step fails.**
The installer prints each step with `→`, a `✓` when it finishes and `✗ <step> failed: <reason>` when it stops, then "Detailed log: <path>". The log is `geass-install-<UTC date and time>.log` in the current directory, or at `GEASS_INSTALL_LOG`, or in the system temp directory when the first place is not writable. Set `GEASS_INSTALL_TRACE=1` to run the K3s installer with `sh -x`. The usual failures:

| Step | Message | Cause and fix |
| --- | --- | --- |
| **system checks** | `GEASS_ACME_EMAIL "<value>" is not an email address` | The value does not look like an address, or is longer than 254 characters. The check runs before anything is changed. Fix the value, or leave the variable out. |
| **wait for k3s ready** | "k3s did not become ready within 5 minutes" | K3s did not start. Read `journalctl -u k3s -n 100`. Common causes are a port already in use or too little memory. |
| **remove existing installation** | "existing k3s installation detected but no uninstall script found …" | Only a `--reinstall` gets here. A K3s install that Geass cannot remove. Remove it yourself, then run the installer again. |
| **check the cluster before updating** | "the cluster is not in a state to be updated", "does not downgrade", "this release cannot replace the custom resource definitions …" | An update found something it should not change the cluster over. Nothing was changed. See [What stops an update](/docs/operations/upgrading/#what-stops-an-update). |
| **wait for the new operator to take over** | "the new operator did not report release …", "the operator pod is not starting: …" | The new operator did not start, or refused to. The installer prints its log and, when it is safe, rolls the operator back to the one that ran before and leaves the cluster running it. See [When the new operator does not take over](/docs/operations/upgrading/#when-the-new-operator-does-not-take-over). |
| **wait for operator ready** | "the operator did not become ready within 5m0s", or "the operator pod is not starting: ..." when its pod is crash looping or cannot pull its image | Run `kubectl -n geass-system describe pod -l control-plane=controller-manager` and read the events. |
| **apply GeassCluster CR** | "wait for GeassCluster CRD: …" | The operator's custom resource definitions did not become established in 60 seconds. Check the operator pod. |
| **store worker join token** | "read join token: …" or "join token file … is empty" | K3s has not written `/var/lib/rancher/k3s/server/node-token`. Check K3s, then run the installer again. |

In multi-provider mode, see [Nodes at more than one provider](/docs/operations/multi-provider/) for the messages about `GEASS_NODE_EXTERNAL_IP` and WireGuard.

:::danger
On a machine that already runs Geass the installer updates it and keeps what it holds; on any other machine that has K3s it stops and says why. It removes a K3s only when you pass `--reinstall`, after a confirmation. Only reinstall a machine you mean to rebuild.
:::

**The installer warns that `GEASS_ACME_EMAIL` is not set.**
The cluster installs and runs, but Geass creates no certificate issuer, so no HTTPS certificate is issued. Set the address on the running cluster; see [A certificate is never issued](#a-certificate-is-never-issued).

**The installer warns "No active host firewall detected; skipping port configuration."**
It found no active `ufw`, `firewalld` or `iptables` rules and opened nothing. That is fine for K3s. If you use a firewall of another kind, or a provider security group, open TCP 6443, 10250, 80 and 443 and the overlay port yourself. The installer opens these ports only in an active host firewall, never in a provider's security group, so open 80 and 443 there for visitors and for certificates.

**The installer warns "The iptables rules that open the ports are not saved".**
The host filters with `iptables` but does not restore rules at boot, so the ports the installer opened close again after a reboot. Install `iptables-persistent` (Debian, Ubuntu) or `iptables-services` (Red Hat family) and save the rules. See [Requirements](/docs/getting-started/requirements/#ports-and-firewall).

**The installer warns "Could not work out this machine's address".**
It found no usable address on the default route, so the cluster's `spec.serverURL` is `https://127.0.0.1:6443`. Geass joins workers on the control plane node's own address instead, so this does not stop you adding servers. To use another address, patch `spec.serverURL` as shown under the loopback-address message in the `GeassNode` section below.

**The cluster is installed but a project's services never start, and `kubectl get geasscluster` shows it not ready.**
See [Everything says the cluster is not ready](#everything-says-the-cluster-is-not-ready).

## The dashboard and sign-in

**The dashboard does not load.**
Check the operator, then your route to it.

```bash
kubectl -n geass-system get deploy geass-controller-manager
kubectl -n geass-system get svc geass-dashboard
```

Until you set a dashboard domain (or use a Cloudflare tunnel), nothing exposes the service outside the cluster. Reach it from your own computer with a port forward, and open `http://localhost:8085`:

```bash
kubectl -n geass-system port-forward svc/geass-dashboard 8085:8085
```

Once a domain is set, check DNS and the ingress (`kubectl -n geass-system get ingress geass-dashboard`) and see [The dashboard domain stays pending](#the-dashboard-domain-stays-pending).

**Signing in says "dashboard authentication is unavailable".**
Geass could not load a login. The usual cause is that none exists: the installer does not create one. Create the Secret `geass-dashboard-auth` in `geass-system` with a `users` key (or set `GEASS_DASHBOARD_PASSWORD` on the operator). The sample is `config/samples/geass_dashboard_auth.yaml`. Geass refuses the placeholder password `CHANGE_ME`, so replace it, and hashes the real one at the first sign-in. See [Sign in to the dashboard](/docs/getting-started/sign-in/).

**Every sign-in says "invalid username or password".**
Five wrong attempts for one user from one address within a minute lock that user for that address for a minute (25 from all addresses together lock it for everyone, and five when addresses cannot be told apart), and the message is the same while locked. Twenty failed sign-ins from one client address within a minute refuse that address for a minute, with the same message. Wait a minute and try once. If it still fails, read the users in the Secret.

**Signing in works, then the dashboard asks for a sign-in again.**
You opened it over plain HTTP at an address other than `localhost`. Geass marks its session cookie `Secure` everywhere except loopback, so the browser drops it. Use HTTPS, or the port forward above. A session lasts 12 hours.

**"viewer role cannot access this".**
Your user has the viewer role. Viewers read, but cannot change anything, read logs, or open a terminal. Ask for the admin role.

**The provider does not work, or nobody can sign in through it.**
Sign in with an admin's password (**Use an admin password instead** on the sign-in page), open **Settings** → **Sign-in** and choose **Switch back to passwords**. If you cannot reach the dashboard at all, `sudo k3s kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"auth":{"mode":"local"}}}'` does the same. Then see the messages below. A user in the Secret who is not an admin cannot sign in with a password while the provider decides; an admin can.

**The sign-in page says "Geass could not complete the sign-in with the identity provider".**
The operator log has the reason (`kubectl -n geass-system logs deployment/geass-controller-manager -c manager | grep -i "identity provider"`). The usual ones: the provider cannot be reached from the cluster, or is on this machine or a link-local address (`the address is this machine or a link-local address`); the issuer you saved is not exactly what its discovery document calls itself (`issuer did not match`, and for Better Auth the issuer includes `/api/auth`); the client secret is wrong (`invalid_client`); the redirect URI is not the one registered at the provider (`redirect_uri` mismatch: use the address on the **Sign-in** page, with the scheme and host the browser used); or the token was signed with an algorithm Geass does not accept (anything but a public-key one).

**The sign-in page says "Your account has no role here" or "not one this installation lets in".**
The provider signed the person in, and Geass has no role for them. **Test sign-in** on the **Sign-in** page shows what the token holds: the role claim's values, and which required claim is missing. Check the claim's name (a dotted path reaches into nested claims), that your **Roles** list has the provider's names exactly as the token spells them, and, for Better Auth, that the callback that adds `org_role` to the ID token runs for this person.

**The sign-in page says "The identity provider did not say what your email address is".**
The ID token has no `email` claim. Better Auth leaves it out of the ID token unless you add it in `customIdTokenClaims`. See [Sign in with an identity provider](/docs/guides/external-sign-in/#better-auth).

**"Use ... to sign in" is disabled, or turning it on answers `400`.**
It needs the settings complete, a test sign-in of exactly the saved settings in the last half hour by someone who maps to admin (a test as a viewer does not count), and an admin with a password in the Secret. Saving the settings again starts the test over. The message says which is missing.

**"Switch to local sign-in before changing the identity provider".**
Settings cannot be edited while people sign in through the provider. Choose **Switch back to passwords**, change them, test, and turn it on again.

**Someone who was removed at the provider can still use the dashboard.**
Their session lasts until it ends (an hour by default) unless the provider sends a back-channel logout to `/auth/oidc/backchannel-logout`. Most providers need that address to be public `https`. Until then, sign them out by revoking what they made under **Settings** → **Tokens**, and lower **Session length**. A logout that arrives ends their sessions but leaves their tokens when **Also revoke the tokens and deploy hooks they made** is off.

## Nodes

Workers are `GeassNode` objects. Check them first:

```bash
kubectl -n geass-system get geassnodes \
  -o custom-columns='NAME:.metadata.name,PHASE:.status.phase,MESSAGE:.status.message'
kubectl get nodes -o wide
```

A node moves through `Pending`, `Joining` and `Ready`. `Failed` carries the reason in its message.

**A `GeassNode` is `Failed`.**

| Message | Cause and fix |
| --- | --- |
| "ssh key secret ref is required for worker nodes" | Set `spec.sshKeySecretRef` to a Secret that holds the worker's private key. |
| "ssh key: …" or "no private key found in secret …" | The Secret is missing, or holds no key under `ssh-privatekey`, `id_rsa`, `private-key` or `key`. |
| "join token: …" | The Secret named by the cluster's `spec.tokenSecretRef` (`geass-token`) is missing, or holds no `token`, `value` or `k3s-token` key. The installer's **store worker join token** step creates it. |
| "cluster not found: …" | `spec.clusterRef.name` names no `GeassCluster`. The installer creates one named `default`. |
| "could not determine the cluster's k3s version: …" | The operator could not list the control plane nodes. Check the operator's log. |
| "ssh dial <address>: …" | The SSH port on the worker (`spec.sshPort`, default 22) is not reachable from the control plane, or the user or key is wrong. |
| "ssh dial <address>: … the SSH host key of … is not the key recorded in known_hosts" (or "… is not listed in known_hosts") | The Secret has a `known_hosts` entry and the machine at `spec.eip` does not match it: another machine answers, the machine was rebuilt, or the entry is for a different address or port. Check the fingerprint on the machine, then update `known_hosts` in the Secret. Without that entry Geass does not check host keys. |
| `sudo: a password is required` in a step's output | A user other than `root` needs passwordless `sudo`. Geass runs each setup step through `sudo -n`. |
| "cannot reach the cluster API at <url> from this node: …" | The worker cannot reach the control plane on the address Geass joins it on. The rest of the message says why: "the connection was refused" and "the connection timed out" mean TCP 6443 is closed between the worker and the control plane, in the host firewall or the provider's security group. "the host name does not resolve" and "something answered, but it is not a K3s API server" mean `spec.serverURL` is wrong. "spec.serverURL on the GeassCluster is a loopback address" is described below. |

**The message says `spec.serverURL on the GeassCluster is a loopback address`.**
The cluster's `spec.serverURL` is `https://127.0.0.1:6443` or similar, which is the control plane itself and means nothing to a worker. The installer now writes the control plane's own address there, and Geass joins workers on the control plane node's address when it finds a loopback one, so this only appears when no control plane node has an address. Set `serverURL` to an address the worker can reach, then delete and re-create the `GeassNode`:

```bash
kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"serverURL":"https://10.0.0.5:6443"}}'
```

**A failed `GeassNode` does not try again after I fix the cause.**
Editing a failed node does not provision it again. Ask for another attempt with the retry annotation: `kubectl -n geass-system annotate geassnode <name> geass.dev/retry=true`. Geass sets the node back to `Pending`, clears its message and removes the annotation. Deleting the node and applying it again also works. Deleting removes the node from the cluster and uninstalls K3s from the machine when Geass can reach it over SSH, on `spec.sshPort`.

**A node stays `Joining`.**
K3s installed but the node did not register under its name. On the worker run `journalctl -u k3s-agent -n 100`. Look for connection errors to port 6443. Geass waits without a time limit.

**A node is `NotReady`, or the rail shows it as down.**
On the machine, check `systemctl status k3s` (the control plane) or `k3s-agent` (a worker). A worker that Geass joined and that stays not ready for five minutes is provisioned again over SSH. See [Usage and capacity](/docs/guides/usage-and-capacity/) for pressure states such as `memory high` and `disk full`.

## Services, databases and buckets that are stuck

### A service stays "Setting up"

The condition is "Service is starting" and does not change. Geass waits until every replica is available, so a service that is only slow stays here. A copy that cannot start does not wait for ever. When a pod is in `CrashLoopBackOff`, `ImagePullBackOff`, `ErrImagePull`, `InvalidImageName`, `CreateContainerConfigError` or `CreateContainerError`, or the Deployment did not become ready within its progress deadline, Geass reports "The service cannot start: `<container>` is `<reason>`" and counts it as a failure, except for a copy that is waiting for the connection Secret of a database or bucket that is still starting: that says "Waiting for database `<name>`" (or "bucket") and is not counted. The label changes to **Unavailable**. After five failures in a row, about two minutes, the service is **Failed**, and the message stays in the deploy's record under **Deployments**.

```bash
kubectl -n morning-beach-production get pods -o wide
kubectl -n morning-beach-production describe pod <pod>
kubectl -n morning-beach-production logs <pod> --previous
```

| What `describe` shows | Cause and fix |
| --- | --- |
| `ImagePullBackOff` or `ErrImagePull` | The image name or tag is wrong, or the registry needs credentials. Check the service's **Source**. |
| `CrashLoopBackOff` | The container starts and exits. Read its logs. Check the port: the service listens on **Port** (default 8080). |
| Readiness probe failed | The health check path returns an error. Check the **Healthcheck path** in **Settings**. |
| `Insufficient cpu` or `Insufficient memory` | The nodes are full. See [A resource says there is not enough capacity](#a-resource-says-there-is-not-enough-capacity). |
| `didn't match Pod's node affinity/selector` | No node carries the project's region, the tags you chose, or the tags the project requires. See [Scaling and placement](/docs/guides/scaling-and-placement/) and [Compliance node tags](/docs/guides/compliance-tags/). |

### A service that sleeps when idle does not sleep, or does not wake

Read its `ScaleToZero` condition and see [Sleep when idle](/docs/guides/scale-to-zero/#troubleshooting). A service woken by a request that cannot start says "The service was woken but its copy cannot start: …", or "The service was woken but has not started within 10m0s".

### A resource is "Unavailable" and names a project or cluster

Geass checks the project before it creates anything in it. The message is one of:

- `project "<name>" does not exist`
- `project "<name>" is not ready`
- `project "<name>" cluster "<cluster>" is unavailable: …`
- `project "<name>" cluster "<cluster>" is not ready`
- `project "<name>" has no <environment> environment`

Create the project first, and wait for it to be ready, before you create resources through manifests. If the project or cluster was briefly not ready, these messages count as failures, and the resource may be **Failed**. Choose **Retry** once the project is ready.

### Everything says the cluster is not ready

A project's condition reads "Referenced GeassCluster is not ready", the resources in it say `project "<name>" cluster "<cluster>" is not ready`, or the cluster's own `Ready` is false with the reason `ClusterPending`.

```bash
kubectl -n geass-system get geasscluster default -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}{"\n"}{end}'
kubectl -n kube-system get helmcharts
kubectl -n cert-manager get pods
kubectl -n monitoring get pods
kubectl -n longhorn-system get pods -o wide
```

`AddonsReady` is false while an add-on is installing or unhealthy: cert-manager, monitoring and, when it is on, Longhorn. Longhorn counts as ready only when every node that can run workloads has a ready Longhorn manager, so a node that joined recently, or one without the iSCSI initiator, holds the whole cluster back until its manager is ready. Fix the add-on, or the node, and the cluster becomes ready by itself. To see why a chart is not installing, read its job: `kubectl -n kube-system get jobs` and `kubectl -n kube-system logs job/helm-install-<chart>`.

### A resource says there is not enough capacity

The message begins "This service needs about …" or "There is no ready node in …". Open **Usage** to see how full the nodes are, then add a node, lower the size, or free something. The messages and fixes are in [Scaling and placement](/docs/guides/scaling-and-placement/). If the resource is already **Failed**, choose **Retry** after the capacity exists.

### A database says "High availability requires a cluster with at least three healthy nodes"

You need three ready nodes that are not cordoned, and a readiness report that says so. See [High availability](/docs/operations/high-availability/), which also explains why **Run readiness check** may seem to do nothing.

### A database sits at "Storage is starting", then "Storage is unavailable"

A new database waits for Longhorn to create its storage. After 15 minutes of Longhorn not being ready, it reports "Storage is unavailable". See [Volumes do not attach](#volumes-do-not-attach).

### The object storage server sits at "Storage is starting", then "Storage is unavailable"

The server waits for Longhorn to create its volume, as a new database does, and reports "Storage is unavailable" after 15 minutes of Longhorn not being ready. A server placed in a region or on tags can also wait without a limit for the storage class of that region or those tags. See [Volumes do not attach](#volumes-do-not-attach) and [Object storage](/docs/guides/object-storage/#troubleshooting).

### A database stays "Deleting"

When a database has backups, deleting it first takes one last backup, so the data can be restored for seven days. Geass waits for it, at most 10 minutes, and then removes the database even if the backup has not finished. If it is still there after that, inspect what holds it:

```bash
kubectl -n geass-system get geassdatabase orders -o jsonpath='{.metadata.finalizers}{"\n"}'
kubectl -n morning-beach-production get cluster.postgresql.cnpg.io,backups.postgresql.cnpg.io
```

## Changes that will not deploy

**I changed a setting and the top bar still says "Up to date".**
Some settings apply the moment you save them and never count as pending: replicas, autoscaling, **Sleep when idle**, the domain and its HTTPS and DNS verification switches, **Enable metrics**, backup settings, and a repository's watch paths. Settings marked **Applies instantly** are these. Everything else (source, port, variables, command, **Resources**, **Run on**) waits for **Deploy changes**.

**There is no Deploy changes button.**
Viewers do not see it. It is also disabled when nothing is pending.

**Choosing Deploy changes shows "No pending changes were deployed. Check the environment filter and resource settings."**
Deploy applies to the environment selected in the environment bar. Select the environment that holds the pending change.

**A deploy is refused with "This service needs about … Scale up the cluster before creating it".**
The capacity check failed. The dashboard counts the whole workload against free room, even on a redeploy. See [Scaling and placement](/docs/guides/scaling-and-placement/).

**The service says "Service has pending changes and no last deployed snapshot".**
The service carries pending-change markers but no record of what was last deployed. This happens to a service edited by hand. Deploy it once from the dashboard.

**The service is Failed after I deployed.**
Fix the cause and choose **Retry**, or change a setting and deploy again. See [How Geass gives up](#how-geass-gives-up-and-how-to-start-again).

## Builds

A service from a Git repository builds before it runs. Each build is a `GeassBuild` in `geass-system`, named `<service>-build-<hash>`. It runs a job named `<build>-kaniko` with two steps: an init container `git` that clones the repository, and a `kaniko` container that builds the Dockerfile and pushes the image. The job is tried up to three times.

```bash
kubectl -n geass-system get geassbuilds
kubectl -n geass-system get geassbuild <build> -o jsonpath='{.status.phase}{"  "}{.status.failureReason}{"\n"}'
kubectl -n geass-system logs job/<build>-kaniko -c git
kubectl -n geass-system logs job/<build>-kaniko -c kaniko
```

In the dashboard, open the service's **Deployments** tab, open the failed deployment, and read **Build Logs**.

A finished build's job, and with it its pod and log, is removed an hour after it finishes. The result stays on the `GeassBuild`, and the log is archived when the service asks for that, so read `kubectl logs` soon or use **Build Logs**. Geass keeps the 10 newest finished builds of a service and deletes older ones, never the one the service runs. A finished build is not run again because its job is gone: start it over with a push to the branch, or with `POST /api/apps/<service>/build`.

| Symptom | Cause and fix |
| --- | --- |
| The service stays not ready with the condition "Git source is waiting for a successful build" | The build is running or queued. Check `status.phase`: `Pending`, `WaitingForCI`, `Running`, `Succeeded`, `Failed` or `Cancelled`. |
| `status.phase` is `WaitingForCI` | **Wait for CI** is on and the commit's checks have not succeeded. Wait for them, or switch **Wait for CI** off in **Source**. |
| `failureReason` "GitHub API returned <status>" or "GitHub did not return a commit for branch …" | Geass turns the branch into a commit through GitHub before it builds, and reads the commit's checks when **Wait for CI** is on. The branch name, the GitHub connection's token, or its access to the repository is wrong. See [GitHub and builds](/docs/guides/github-and-builds/). |
| The `git` container fails | The branch or repository name is wrong, or the connection cannot read a private repository. |
| `failureReason` "kaniko Job failed" | The build itself failed. Read the `kaniko` log: a Dockerfile error, a wrong **Dockerfile path** or root directory, or a failed push. |
| `failureReason` "Kaniko completed without an immutable image digest" | The build finished but Geass found no digest: the `kaniko` container's termination message was empty and its log has no `Pushed <repository>@sha256:…` line. Read the `kaniko` log for a push error. A build that failed this way before Geass read the termination message did push its image: run it again with a push or `POST /api/apps/<service>/build`. |
| `failureReason` "no container registry is set for builds" | Neither the service nor the platform names a registry. See below. |
| No `geass/build/<service>` status, GitHub Deployment or pull request comment on GitHub | The build and the deploy are fine: Geass reports back to GitHub on the side and never holds them up for it. Look at the project's GitHub connection: `kubectl -n geass-system get geassgithubconnection <project>-github -o yaml`. A `GitHubReporting` condition that is `False` with the reason `PermissionMissing` means the GitHub App was registered before Geass reported to GitHub and lacks **Commit statuses**, **Deployments** or **Pull requests** at read and write. **Test GitHub App** in **Settings** → **Connectors** lists them. See [Give an existing GitHub App the new permissions](/docs/guides/github-and-builds/#give-an-existing-github-app-the-new-permissions). Also check that **Report to GitHub** is on in the project's **Integrations**. |

:::note
Geass builds push to a registry that you configure, and Geass has none of its own. Without one the service does not build and shows `no container registry is set for builds`. The destination comes from the service's `spec.build.registry.repository`, or, for every service, from `spec.registry.repository` on the `platform` object. An admin sets the platform registry with the REST API (the dashboard has no form for it yet), which also stores the login as a Docker registry Secret:

```bash
curl --fail -X POST https://geass.example.com/api/settings/registry/save \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d repository=ghcr.io/example/builds -d username=builder --data-urlencode password="$REGISTRY_TOKEN"
```

Or with `kubectl`, using a Secret of type `kubernetes.io/dockerconfigjson` in `geass-system`, named in `credentialRef`:

```bash
kubectl -n geass-system patch geassplatformconfig platform --type merge \
  -p '{"spec":{"registry":{"repository":"ghcr.io/example/storefront","credentialRef":{"name":"registry-credentials"}}}}'
```

The `platform` object exists once you have saved a dashboard domain or connected GitHub or Cloudflare; the patch fails before that. Geass passes the repository value to Kaniko as the image to push, so set it per service when different services need different images. The nodes pull the built image with a copy of the same login, kept as the Secret `<service>-registry-pull` in the environment namespace.
:::

## Domains and certificates

### The dashboard domain stays pending

Open **Settings** and then **Domain** to see the domain you saved. That page does not show the verification status. Read it from the `platform` object:

```bash
kubectl -n geass-system get geassplatformconfig platform -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}: {.message}{"\n"}{end}'
```

| Reason | Message | Fix |
| --- | --- | --- |
| `NoDomain` | "Dashboard domain is not configured" | Set a root domain. |
| `PendingDNS` | "DNS not detected yet" | The dashboard's host does not resolve to the server's public address. Add the DNS record and wait. `dig +short geass.example.com` should print the server's public IP. |
| `PendingDNS` | "could not determine this server's public IP" | Geass found no public address on the nodes and could not look one up. Set the record by hand, or check the server's outbound access. |
| `PendingDNS` | "Could not create dashboard DNS record in Cloudflare" | The Cloudflare connection cannot write the record. Check its token under **Settings** and then **Connectors**. |
| `PendingHTTPS` | "Dashboard HTTPS endpoint is not reachable yet" | DNS is right but HTTPS does not answer. When the cluster's certificate issuer is the problem, the message ends with the reason in parentheses, for example "(No ACME contact email is configured, so the letsencrypt-prod ClusterIssuer was not created …)". See [A certificate is never issued](#a-certificate-is-never-issued). |
| `PendingTunnel` | "Cloudflare tunnel ID is not configured" | Finish the Cloudflare tunnel settings. |
| `Error` | "Cloudflare credentials are unavailable" or "… are incomplete" | Reconnect Cloudflare under **Settings** and then **Connectors**. |
| `ZoneRequired` | "Select a Cloudflare zone in domain settings (multiple zones match this token)" | Choose the zone. |
| `VerificationTimedOut` | "Not verified after 15 minutes: …" | The cause follows the colon. Geass keeps checking every five minutes, so fixing it still turns the domain green. |

### A service's HTTPS domain does not work

1. Check DNS: the host must resolve to a node that serves ports 80 and 443. `dig +short app.example.com`.
2. Check the ports. The installer opens 80 and 443 only in an active host firewall on the control plane, never in the provider's security group, and never on workers. Open them on every node that serves web traffic and in the provider's security group.
3. Check that the cluster has a certificate issuer: `kubectl -n geass-system get geasscluster default -o jsonpath='{.status.conditions[?(@.type=="ClusterIssuerReady")].message}'`. See [A certificate is never issued](#a-certificate-is-never-issued).
4. Check the ingress and certificate:

   ```bash
   kubectl -n morning-beach-production get ingress,certificate
   kubectl -n morning-beach-production describe certificate <service>-tls
   ```

### A certificate is never issued

Every HTTPS ingress, including the dashboard's, asks cert-manager for its certificate from a ClusterIssuer named `letsencrypt-prod`. Geass creates that issuer itself, but only once it has an email address for the Let's Encrypt account.

:::caution
Without an ACME email address, Geass creates no issuer and no HTTPS certificate is issued. The cluster still installs and runs; the dashboard domain stays on `PendingHTTPS` and every service's HTTPS stays unavailable. You give the address to the installer as `GEASS_ACME_EMAIL`. Let's Encrypt refuses placeholder domains such as `example.com`.
:::

**What Geass creates, and when.** When cert-manager is ready and `spec.addons.certManager.acmeEmail` is set on the cluster (the installer writes it from `GEASS_ACME_EMAIL`), the operator creates the `letsencrypt-prod` ClusterIssuer. It is an ACME issuer for Let's Encrypt's production server (`https://acme-v02.api.letsencrypt.org/directory`), with the account key in the Secret `letsencrypt-prod-account-key`, and it solves the HTTP-01 challenge through the `traefik` ingress class. The operator puts the issuer back if you delete or edit it, and updates it when you change the address. It looks again every 30 seconds while cert-manager is installing or the account is registering.

Geass leaves some things alone:

- A `letsencrypt-prod` issuer that you created yourself, including one made by hand before Geass created its own, is never overwritten. It is reported as `ExternallyManaged`.
- Taking the address away later does not delete the issuer Geass made, because certificates issued through it still renew through it.
- With cert-manager switched off (`spec.addons.certManager.enabled: false`), Geass manages no issuer at all, and it removes the cert-manager it had installed. You run cert-manager and the issuer yourself.
- A dashboard published through a Cloudflare tunnel gets its certificate from Cloudflare, so the issuer is not involved there.

To set or change the address on a running cluster, including one installed without it:

```bash
kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"addons":{"certManager":{"acmeEmail":"you@example.com"}}}}'
```

Use a real address in place of `you@example.com`. Geass creates or updates the issuer within seconds.

**Check what Geass says.** The cluster has a `ClusterIssuerReady` condition. It is separate from the cluster's `Ready`, so a cluster can be ready with no working issuer.

```bash
kubectl -n geass-system get geasscluster default \
  -o jsonpath='{.status.conditions[?(@.type=="ClusterIssuerReady")]}{"\n"}'
kubectl get clusterissuer letsencrypt-prod
kubectl describe clusterissuer letsencrypt-prod
```

| Reason | Meaning | Fix |
| --- | --- | --- |
| `ACMEEmailMissing` | No address is set, so the issuer was not created. | Patch the cluster as above. |
| `WaitingForCertManager` | cert-manager is not ready, or its API is not served yet. | Wait. See [Everything says the cluster is not ready](#everything-says-the-cluster-is-not-ready). |
| `IssuerRegistering` | The issuer exists and cert-manager has not finished registering the ACME account. | Wait a minute. |
| `IssuerNotReady` | cert-manager reports the issuer is not ready, and the message ends with its reason. This also applies to an issuer you created yourself. The likeliest reason is an address that Let's Encrypt refuses, or no route from the cluster to Let's Encrypt. | Set a real address, and check that the server can reach the internet. |
| `IssuerApplyFailed` | The operator could not create or update the issuer. It retries every 30 seconds. The error is in the operator's log, not in the message. | `kubectl -n geass-system logs deploy/geass-controller-manager -c manager --tail=100`. |
| `IssuerAccessDenied` | The operator's cluster role lacks permission on `clusterissuers.cert-manager.io`. | Apply the operator's current manifests, which grant get, list, watch, create, update and patch. |
| `ExternallyManaged` | A `letsencrypt-prod` issuer exists that Geass did not create. It is left unchanged. | Its own settings decide whether certificates work. To let Geass manage it, delete yours. |
| `ManagedByAnotherCluster` | Another `GeassCluster` owns the issuer, which is cluster-wide. Geass leaves it as it is. | Change the address on the cluster that owns it. |
| `IssuerReady` | The issuer works. | If certificates still fail, see the next list. |

While HTTPS is not reachable, the same message is appended to the dashboard domain's `PendingHTTPS` status.

**If the issuer is ready and certificates still fail.** Work through these in order:

1. **DNS.** The host must resolve to the public address of a node that serves ports 80 and 443. Let's Encrypt connects to that address to validate the domain.
2. **Ports 80 and 443.** Port 80 carries the HTTP-01 validation and port 443 carries visitors. The installer opens both in an active host firewall on the control plane, but not in the provider's security group and not on workers. Open them there.
3. **The address.** An address at a domain Let's Encrypt refuses stops the account from registering. The issuer then shows `IssuerNotReady`.
4. **Rate limits.** Let's Encrypt limits how often it issues and how many times a validation may fail. After repeated failures, the order or certificate reports a rate-limit error. Fix the cause, then wait before trying again.

To see where a certificate is stuck, follow the chain. For each ingress, cert-manager creates a Certificate named after the TLS Secret (`<service>-tls`, and `geass-dashboard-tls` in `geass-system` for the dashboard). The Certificate makes a CertificateRequest, an Order, and one Challenge for each host. These objects are in the namespace of the ingress:

```bash
kubectl get certificate -A
kubectl -n morning-beach-production describe certificate web-tls
kubectl get certificaterequests,orders.acme.cert-manager.io,challenges.acme.cert-manager.io -A
kubectl describe challenge -A
```

A challenge's `State` and `Reason` name the failure, for example a self-check that cannot fetch the challenge URL (DNS, or port 80 blocked). While a challenge runs, cert-manager serves it from a short-lived Pod, Service and Ingress named `cm-acme-http-solver-…` in the same namespace. cert-manager itself must be running: `kubectl -n cert-manager get pods`.

## Backups

A database with backups on shows one of four states on its **Backups** tab:

| State | Message | Cause and fix |
| --- | --- | --- |
| **Needs storage** | "Choose where backups are stored to protect this database" | The project has no backup storage. Open the project's **Settings**, then **Backups**, and choose an AWS connection. |
| **Needs storage** | "Backup storage is not working: …" | The bucket or its credentials fail. Read the reason after the colon, and check the connection under **Settings** and then **Connectors**. |
| **Starting** | "Taking the first backup" | The first backup has not finished. Wait. |
| **At risk** | "Recent changes are not being saved to backup storage" | Postgres is not archiving its log to the bucket. Check storage and connectivity. |
| **At risk** | "The latest backup failed" | The last scheduled backup failed. Check storage, then wait for the next one. |
| **Protected** | "Backups are running" | Healthy. |

```bash
kubectl -n geass-system get geassdatabase orders -o jsonpath='{.status.backup}{"\n"}'
kubectl -n geass-system get geassbackuparchives
kubectl -n morning-beach-production get backups.postgresql.cnpg.io,scheduledbackups.postgresql.cnpg.io   # Postgres
kubectl -n morning-beach-production get cronjob,job -l geass.dev/backup-for=orders                       # MySQL and SQLite
```

Postgres backs up continuously. MySQL and SQLite take a copy daily, between 02:00 and 02:59 UTC at a minute that depends on the database. See [Backups and restore](/docs/guides/backups-and-restore/).

## Volumes do not attach

Database storage, and the object storage server's, use Longhorn, which needs the iSCSI initiator (`iscsid`) on every node. Signs of trouble: a database or the object storage server at "Storage is starting" or "Storage is unavailable", a pod `Pending` with a volume error, or the cluster not ready with `AddonsReady` false.

```bash
kubectl get storageclass
kubectl -n morning-beach-production get pvc
kubectl -n morning-beach-production describe pvc <claim>
kubectl -n longhorn-system get pods -o wide
```

| Finding | Cause and fix |
| --- | --- |
| No `geass-replicated` class | Longhorn is not ready. Geass creates the classes only after its driver is registered and every node has a ready manager. |
| A `longhorn-manager` pod not ready on one node | That node lacks the iSCSI initiator. The installer and the worker setup install it on a best-effort basis and carry on if they cannot. On Debian and Ubuntu run `sudo apt-get install -y open-iscsi && sudo systemctl enable --now iscsid`. On other systems install `iscsi-initiator-utils` (Red Hat family) or `open-iscsi` (SUSE), then enable `iscsid`. |
| The installer logged "Could not set up the iSCSI initiator, so database storage will not work on this machine until it is installed" | The same cause on the control plane. Look in the installation log. |
| A database in a region waits for a regional class | Every node in the region needs a ready manager. See [Scaling and placement](/docs/guides/scaling-and-placement/). |
| A database of a project that requires node tags waits for its class | Every node that carries the tags needs a ready manager, then Geass creates `geass-tagged-<hash>`. See [Compliance node tags](/docs/guides/compliance-tags/). |

See [Storage](/docs/guides/storage/).

## Related

- [Usage and capacity](/docs/guides/usage-and-capacity/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Sleep when idle](/docs/guides/scale-to-zero/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [High availability](/docs/operations/high-availability/)
- [Nodes at more than one provider](/docs/operations/multi-provider/)
- [Logs, metrics and terminal](/docs/guides/observability/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [Upgrading](/docs/operations/upgrading/)
