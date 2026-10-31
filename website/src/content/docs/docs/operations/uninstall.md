---
title: "Uninstall Geass"
description: "Remove Geass from your servers: the order to follow, what is deleted and what stays, and the cloud resources that you must clean up yourself."
---

Geass has no uninstall command. To remove Geass, do these steps in this order:

1. [Delete your resources from the dashboard](#step-1-delete-resources-from-the-dashboard), so that Geass removes what it created outside the cluster.
2. [Disconnect the connectors](#step-2-disconnect-connectors).
3. [Remove the other servers](#step-3-remove-workers).
4. [Uninstall the control plane server](#step-4-uninstall-the-control-plane).

Geass removes some external resources only when you delete the matching resource in the dashboard first. Geass never removes other external resources, for example DNS records and the backup bucket.

## Before you uninstall

- Dump the data that you want to keep with the tools of each database. Copy the contents of your buckets. The backups in S3 are not enough, because the dashboard restores from records that are inside the cluster.
- Decide which external resources to keep, for example S3 buckets, DNS names, and PlanetScale databases.
- Do the cleanup in the next steps while the dashboard still works. After Geass is gone, nothing can delete a bucket, an IAM user, or a cloud machine for you.

## What is deleted and what stays

| Thing | After you uninstall | Notes |
| --- | --- | --- |
| Projects, services, databases, secret variables, settings, dashboard users, and API tokens | Deleted | They are in the cluster's state, which the uninstall deletes. |
| Data on volumes without replicated storage | Deleted | |
| Data on replicated volumes | Stays on disk | Nothing uses it. See [Clean up the servers](#clean-up-the-servers). |
| S3 buckets and IAM users of your buckets | Stay, unless you deleted the bucket in Geass first | See [What deleting each resource does](#what-deleting-each-resource-does). |
| The backup bucket, its IAM user, and the backups in it | Stay | Geass never deletes the backup bucket. |
| Cloudflare DNS records and the Cloudflare Tunnel | Stay | Delete them in Cloudflare. |
| The GitHub App | Stays | Delete it in GitHub. |
| PlanetScale databases and passwords | Stay | |
| Other servers | Keep running | Remove them in [Step 3](#step-3-remove-workers). |
| Machines that Geass created at a cloud | **Keep running and keep costing money** | Delete them in [Step 3](#step-3-remove-workers), while Geass still runs. Nothing else deletes them. |
| Images that your builds pushed to a registry | Stay | |
| Firewall rules and packages that the installer added | Stay | See [Clean up the servers](#clean-up-the-servers). |
| Saved `geass` logins, tokens in CI secrets, and `geass mcp` entries | Stay | See [Clean up your own machines](#clean-up-your-own-machines). |

## Step 1: delete resources from the dashboard

Delete one kind of resource at a time. Each resource shows **Deleting** until Geass finishes its cleanup. Wait until each resource disappears before you continue.

1. **Services.** Open the service settings, and select **Danger**. Enter the service ID, click **Delete service**, and click **Delete permanently**.
2. **Databases.** Do the same with **Delete database**. If backups are on, Geass first takes a last backup, and waits up to 10 minutes for it. Geass keeps the backups for 7 days. Without backups, the data is gone.
3. **Buckets.** Do the same with **Delete bucket**.
4. **Projects.** In the project **Settings**, select **Danger**. Under **Delete Project**, enter the project name, click **Delete**, and click **Delete permanently**.

The **Manage Services** list on the same page shows each resource of the project with a **Remove** button. You can use it to delete the resources one at a time.

:::caution
Delete buckets and databases before you delete their project, and wait until they are gone. Geass deletes the AWS and PlanetScale connections of a project together with the project. Geass needs the AWS connection to delete an S3 bucket and its IAM user. Without the connection, the bucket stays in **Deleting**, and its log shows `AWS connection is unavailable`.
:::

When you delete a project, the access key of each AWS connection stays in the cluster until you uninstall. Revoke the key in AWS.

### What deleting each resource does

| Resource | What Geass removes | What stays |
| --- | --- | --- |
| Service | The service, its domains, its variables and secret variables, its deploy history, and the schedules of a function | Images in a registry. Cloudflare DNS records for its domains. |
| Database in the cluster | The database and its data | Backups in S3 for 7 days. After that, Geass deletes them while it runs. |
| External database (PlanetScale, AWS RDS) | The connection details in Geass | The database itself. |
| Database on a shared server | The database and its login, within 3 minutes | |
| Bucket in the cluster | The bucket, its objects, and its login | |
| S3 bucket that Geass created | The bucket, every object and version, and its IAM user and keys | |
| S3 bucket that you attached from your account | The IAM user and keys that Geass made | The bucket and its contents. |
| Project | All of the above for the project, its environments, its shared variables, its GitHub link, its API tokens, and its cloud connections | External resources whose cleanup needed a connection that was already gone. |

A draft that you never deployed has nothing outside the cluster to remove.

You cannot delete the object storage server in **Settings** > **Object storage** while buckets use it. When you delete it, Geass deletes everything on it. You cannot delete a shared database server in **Settings** > **Databases** while databases use it.

## Step 2: disconnect connectors

Open **Settings** > **Connectors**, and do these steps:

- **Cloudflare.** Under **Disconnect Cloudflare**, enter `remove-cloudflare`, and click **Disconnect**. Geass stops its tunnel and its DNS updates. Your Cloudflare tunnel named `geass` and the DNS records stay. Delete them in Cloudflare:
  - The `A` record for the dashboard host. By default, the host is `geass.<your domain>`.
  - The `CNAME` records that point at the tunnel, for the dashboard and for your services.
  - The tunnel.
- **GitHub.** Enter `remove-github`, and click **Remove GitHub App**. Geass forgets the app. The GitHub App and its installations stay. Delete them in your GitHub settings.
- **AWS and PlanetScale.** Geass removed these connections with their projects. Revoke the access keys and tokens at the provider.
- **Server clouds.** Do this after [Step 3](#step-3-remove-workers). Geass does not remove a connection while servers or pools use it, because it needs the connection to delete the machines. Click **Remove** on each connection under **Server clouds**, or run `geass connections remove NAME`. The credentials stay valid at the cloud until you revoke them there.

## Step 3: remove workers

Remove the other servers while the control plane server still runs.

1. Delete each [pool](/docs/guides/node-pools/). Open the pool, and click **Delete pool**. Or run `geass pools delete NAME`.
2. Open **Usage** in the left rail.
3. In the **Nodes** table, click **Remove** on a server.
4. Enter the server name, and click **Remove server**. You can also run `geass servers remove NAME`.
5. Do steps 3 and 4 for each server that has a **Remove** button.
6. Wait until the servers are gone from the **Nodes** table, or from `geass nodes list`.

For a machine that Geass created at a cloud, Geass moves its services for up to five minutes. Then Geass removes the server and deletes the machine at the cloud. For a server that you added over SSH, Geass uninstalls its agent over SSH, and removes the server.

:::caution
Remove the cloud machines before you uninstall the control plane server. After the uninstall, nothing can delete them, and they continue to cost money. When you finish, look in the console of each cloud for machines with the labels `geass-managed` and `geass-cluster`.
:::

Geass cannot remove a server that has no **Remove** button, or that Geass cannot reach. See [Under the hood](#under-the-hood).

See [Add servers](/docs/getting-started/add-servers/#remove-a-node) for what removal does to the services on a server.

## Step 4: uninstall the control plane

Geass has no dashboard option or command to uninstall the control plane yet. See [Under the hood](#under-the-hood).

This step deletes K3s, the cluster's state, and the local volumes. You cannot get the cluster back after this step. If the cluster has [three control plane servers](/docs/operations/high-availability/#run-three-control-plane-servers), do this step on each of them.

## Clean up the servers

The uninstall leaves these items on each server: replicated volume data, the firewall rules that the installer added, Tailscale, the iSCSI packages, and the installer files. Geass has no command to remove them. See [Under the hood](#under-the-hood).

Outside the servers, remove these items:

- **Firewall rules at your provider.** Delete the rules for Geass in the security group of your provider, for example for 80/tcp and 443/tcp.
- **A Tailscale mesh.** Remove the servers in the Tailscale or Headscale admin console. Revoke the auth key that you gave Geass.

## Clean up your own machines

The uninstall deletes every API token. But the tokens are also saved on other machines.

- **Command line.** On each machine where you ran `geass login`, run `geass logout`. Or delete the saved login file:
  - On Linux: `~/.config/geass/config.json`.
  - On macOS: `~/Library/Application Support/geass/config.json`.
  - On Windows: `%AppData%\geass\config.json`.
  - Or the path in `GEASS_CONFIG`.

  To remove the program, delete the `geass` file from `/usr/local/bin` or `~/.local/bin`.
- **CI and scripts.** Delete `GEASS_URL` and `GEASS_TOKEN` from the secret store of your CI system. Delete the jobs that call `geass`.
- **AI agents.** Remove the `geass mcp` entry from each agent host. For example, run `claude mcp remove geass` in Claude Code. Stop any `geass mcp --listen` server.

## Check what is left in other accounts

| Where | Look for |
| --- | --- |
| AWS S3 | Buckets whose deletion did not finish. The backup bucket, named `geass-backups-` and 12 hex characters by default. |
| AWS IAM | Users whose names start with `geass-`. Delete their access keys and the users. |
| Each cloud that made machines | Machines labeled `geass-managed=true` (on OVHcloud, named `geass-...`), firewalls named `geass-<cluster>`, and the credentials of the connection. A firewall rule that Geass added for the control plane has the description `geass` (AWS) or `geass-access` (Hetzner). |
| Cloudflare | DNS records for the dashboard and your services, the tunnel `geass`, and the API token that you gave Geass. |
| GitHub | The GitHub App and its installations. |
| PlanetScale | Databases and passwords that Geass created, and the service token. |
| Your DNS provider | Records that you created yourself. |

## Troubleshooting

**A resource stays in Deleting.** Geass retries its cleanup until it succeeds. The usual causes are these:

- An AWS connection that is gone.
- AWS credentials that expired or were revoked.
- Buckets that still use the object storage server.

Fix the cause, and the deletion finishes. To leave the external resource behind and stop the cleanup, see [Under the hood](#under-the-hood).

**A server comes back in the Nodes table after you removed it.** Geass could not reach the machine, so its agent still runs. See [Under the hood](#under-the-hood).

**Disk space is not freed after the uninstall.** The replicated volume data is still there. See [Clean up the servers](#clean-up-the-servers).

## Under the hood

Each Geass resource has a finalizer. Kubernetes keeps the object in **Deleting** until Geass finishes its cleanup and removes the finalizer. To see what waits, and why, run these commands on the control plane server:

```bash
sudo k3s kubectl -n geass-system get geassapps,geassdatabases,geassobjectstores,geasslogicaldatabases,geassprojects
sudo k3s kubectl -n geass-system logs deploy/geass-controller-manager -c manager
```

To stop a cleanup permanently and leave the external resource behind, remove the finalizer:

```bash
sudo k3s kubectl -n geass-system patch geassobjectstore <name> --type merge -p '{"metadata":{"finalizers":null}}'
```

### Commands on the servers

The dashboard and the command line cannot do these tasks.

**Remove a worker that Geass cannot remove.** On a server that has no **Remove** button, or that Geass cannot reach, run this command:

```bash
sudo /usr/local/bin/k3s-agent-uninstall.sh
```

**Uninstall the control plane.** On the control plane server, run these commands. On a cluster with three control plane servers, run them on each of them. The first command removes the service that keeps the dashboard on `127.0.0.1:8085`. On a server without systemd, skip it:

```bash
sudo systemctl disable --now geass-dashboard && sudo rm -f /etc/systemd/system/geass-dashboard.service
sudo /usr/local/bin/k3s-uninstall.sh
```

The command stops K3s and deletes its files, the cluster's state, and its local volumes. If the command says `k3s-uninstall.sh: No such file or directory`, the machine is not a control plane server. On a worker, run `k3s-agent-uninstall.sh`.

**Clean up each server.** Remove these items:

- **Replicated volume data.** Run `sudo rm -rf /var/lib/longhorn`. This deletes the only copies of the database volumes on that server. Do it only when you are sure that you do not need them.
- **Firewall rules.** If `ufw` or `firewalld` is active, the installer opened 6443/tcp, 10250/tcp, and 8472/udp. On a multi-site cluster, it opened 51820/udp. Delete these rules, for example with `sudo ufw delete allow 6443/tcp`. Also delete the rules that you added yourself, for example for 80/tcp and 443/tcp.
- **A Tailscale mesh.** Tailscale stays installed, and each server stays on the tailnet. Run `sudo tailscale logout` on each server.
- **iSCSI.** The `iscsid` service and the `open-iscsi` or `iscsi-initiator-utils` package stay installed.
- **Installer files.** Delete `geass-install-*.log` in the directory where you ran the installer, and the downloaded installer in your temporary directory.

### Details

- A server added over SSH is a `GeassNode` in `geass-system`. The **Remove** button deletes it. Geass then runs `/usr/local/bin/k3s-agent-uninstall.sh` on the machine over SSH.
- The access key of an AWS connection is in the Secret `<connection>-credentials` in `geass-system`. Deleting the project does not delete that Secret.
- Geass does not delete the volume claims of a MySQL database. They go when the project's namespace is deleted.
- The cluster's state is the embedded etcd database in `/var/lib/rancher/k3s`. `k3s-uninstall.sh` deletes it, with `/etc/rancher/k3s` and `/var/lib/kubelet`. Longhorn data is in `/var/lib/longhorn`.
- Geass never uninstalls KEDA or the admission policies of compliance tags. Only the uninstall of K3s removes them.

## Related

- [Upgrade Geass](/docs/operations/upgrading/)
- [Add servers](/docs/getting-started/add-servers/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Object storage](/docs/guides/object-storage/)
- [Cloud connectors](/docs/guides/connectors/)
- [Command line](/docs/guides/command-line/)
