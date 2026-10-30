---
title: "Uninstall Geass"
description: "Remove Geass from your servers: the order to follow, what happens to workloads, volumes, and backups, and the cloud resources that you must clean up."
---

Geass has no uninstall command. You remove Geass in this order:

1. Delete your resources from the dashboard, so that Geass cleans up what it created in other places.
2. Remove the workers.
3. Uninstall K3s with the K3s scripts.

This page gives that order. It also says what is deleted and what is left behind, based on what the code does. The important part: Geass cleans up some external resources only when you delete the matching resource first. Geass never removes other external resources, for example DNS records and the backup bucket.

## What happens to what

| Thing | When K3s is uninstalled | Notes |
| --- | --- | --- |
| The operator, the dashboard, every Geass object, secrets, dashboard users, and API tokens | Deleted | They are in the cluster's database. K3s's uninstall script removes that database with its data directory |
| Workloads and their namespaces | Deleted | |
| The add-ons and what the operator created for them: KEDA and its HTTP add-on (the `keda` namespace), the operator's `ServiceMonitor` and `PrometheusRule`, the admission policies for compliance tags, and the `CronJob` objects of function schedules | Deleted | They are objects in the cluster's database. Geass itself never uninstalls KEDA or the admission policies, so only the removal of the cluster removes them |
| Volumes on K3s's `local-path` storage (metrics, log history, and the in-cluster object storage server and databases created without Longhorn) | Deleted | They are under K3s's data directory (`/var/lib/rancher/k3s` by default) |
| Longhorn replica data (database and object storage volumes) | Left on disk | The data is in Longhorn's data directory (`/var/lib/longhorn/` by default) on each node. Nothing refers to the data anymore. Remove it yourself |
| iSCSI initiator packages that the installer added | Left | |
| Firewall rules that the installer added | Left | |
| S3 buckets and IAM users of your **buckets** | Left, unless you deleted the bucket in Geass first | See [Buckets](#what-deleting-each-resource-does) |
| The S3 bucket and IAM user for **backups**, and the backups in it | Left | Geass never deletes the backup bucket or its user. Geass deletes the backups of a deleted database after 7 days, but only while the operator runs |
| Cloudflare DNS records and the Cloudflare Tunnel | Left | Geass has no code that deletes them |
| The GitHub App | Left | Delete it in GitHub |
| PlanetScale databases and passwords | Left | |
| Worker servers | K3s agents keep running | Remove workers first, or run the agent uninstall script on each worker |
| Machines that Geass created at a cloud (pools and servers added with a cloud connection) | **Keep running and keep billing** | Delete the pools and servers first, while the operator and the connection still work. Only this step deletes the machines. See [Step 3](#step-3-remove-workers) |
| Images that your builds pushed to a registry | Left | |
| The `geass` command line's saved logins on your machines, the tokens in your CI secrets, and the `geass mcp` entries in AI agent hosts | Left | They hold tokens that the deleted cluster no longer recognizes. See [Clean up your own machines](#clean-up-your-own-machines) |

## Before you uninstall

- Use the databases' own tools to dump any data that you want to keep. Copy the contents of in-cluster buckets. Backups alone are not enough to rebuild, because the dashboard restores from records that are in the cluster that you are about to delete. See [Upgrade Geass](/docs/operations/upgrading/) for the reason.
- Decide which external resources to keep: S3 buckets, DNS names, and PlanetScale databases.
- While the dashboard still works, do the cleanup in the next sections. After the operator is gone, nothing can delete a bucket or an IAM user for you.

## Step 1: delete resources from the dashboard

Delete resources one kind at a time. Wait until each resource disappears before you continue. Each resource shows **Deleting** until its cleanup finishes.

1. **Services.** Open the service, and go to **Delete service** in its **Settings**. Enter the service ID, click **Delete service**, and confirm with **Delete permanently**.
2. **Databases.** In the same section, use **Delete database**. If backups were on, Geass first takes a last backup, and it waits up to 10 minutes for that backup. Then Geass keeps the backups restorable for 7 days. Without backups, the data is destroyed.
3. **Buckets.** Use **Delete bucket**.
4. **Projects.** In the project's **Settings → Danger**, enter the project name under **Delete Project**. Click **Delete**, and confirm. The same page has **Manage Services**, which lists every resource of the project with a **Remove** button. You can use that list to delete the resources one by one first.

:::caution
Delete buckets and databases before you delete their project, and wait for them to finish. When you delete a project, Geass also deletes the project's cloud connections (AWS and PlanetScale) in the same pass. The cleanup of an S3 bucket needs the AWS connection to delete the bucket and its IAM user. The purge of an old database's backups also needs the AWS connection. If the connection of a bucket is already gone, Geass cannot clean up that bucket. The bucket's object stays in `Deleting` and retries with the error `AWS connection is unavailable`, and the S3 bucket and IAM user remain. This is what the code does, and the order above prevents it.
:::

When you delete a project, Geass does not delete the credentials Secrets of its connections. A connection named `prod-aws` keeps a Secret named `prod-aws-credentials` in `geass-system`, with the access key inside. Delete that Secret yourself, and revoke the key in AWS.

### What deleting each resource does

| Resource | What Geass removes | What it leaves |
| --- | --- | --- |
| Service | The Deployment, Service, Ingress, autoscaler, metrics monitor, and variables that it created, its deployment history, and its `<service-id>-secrets` Secret. For a service that sleeps when idle, its `<service-id>-wake` Service and `HTTPScaledObject`. For a function, its schedules (`CronJob` objects) and its `<service-id>-trigger` Secret | Images that builds pushed to a registry. Cloudflare DNS records for its hostname |
| In-cluster database | The database server, its Service, and its connection Secrets. PostgreSQL and SQLite volumes go with it. Longhorn volumes use a `Delete` reclaim policy. Redis keeps no volume | Backups in S3, for 7 days. After that, Geass purges them while the operator runs. MySQL's volume claims: Geass does not delete them, so check `kubectl get pvc -n <project-id>-<environment>`. They are deleted when the project's namespace is deleted |
| External database (PlanetScale, AWS RDS) | Geass's own connection details | The database itself. Geass has no code to delete a PlanetScale or RDS database |
| Logical database on a shared server | The database and its login on the shared server, within 3 minutes | |
| In-cluster bucket | The bucket with its objects, and the bucket's login on the object storage server | |
| AWS S3 bucket created by Geass | The bucket with every object, version, and unfinished upload, and the scoped IAM user with its access keys and policy | |
| AWS S3 bucket attached from your account | The scoped IAM user with its keys and policy | The bucket and its contents |
| Project | All of the above for the project. Also the project's namespaces and everything in them, its shared-variable and webhook secrets, its GitHub link, its API tokens, and its cloud connections | The credentials Secrets of the connections. External resources whose cleanup needed a connection |

A bucket or a database that was only a draft, and that was never deployed, has nothing outside the cluster to remove.

You cannot delete the in-cluster object storage server, SeaweedFS or Garage (**Settings → Object storage**), while project buckets exist on it. When you delete the object storage server, Geass deletes everything on its volume, and its network policy `geass-s3`. You cannot delete a shared database server (**Settings → Databases**) while databases are on it.

## Step 2: disconnect connectors

In **Settings → Connectors**, do these steps:

- **Cloudflare → Disconnect.** Enter `remove-cloudflare`. This stops the `cloudflared` deployment and the automatic DNS updates. Your Cloudflare account, the tunnel named `geass`, and every DNS record that Geass created stay. Delete these items in Cloudflare:
  - The proxied `A` record for the dashboard host (`geass.<your domain>` by default).
  - The `CNAME` records that point at the tunnel (dashboard and service hostnames, in Tunnel mode).
  - The tunnel.
- **GitHub → Remove GitHub App.** Enter `remove-github`. This removes Geass's copy of the app's credentials. The GitHub App itself and its installations stay. Delete them in your GitHub settings.
- **AWS and PlanetScale.** Geass removes these connections with their project (see above). Revoke the access keys and tokens at the provider.
- **Clouds for machines.** Do this after [Step 3](#step-3-remove-workers), not before. You cannot remove a connection from the dashboard while pools or machines still use it, because without the connection Geass could not delete the machines. Click **Remove** on each connection under **Server clouds**, or run `geass connections remove NAME`. This deletes the connection and its Secret. The credentials stay valid at the cloud until you revoke them there.

## Step 3: remove workers

**Machines that Geass created at a cloud come first.** Delete every [pool](/docs/guides/node-pools/) with `geass pools delete NAME`, or with **Delete pool** on its page. Delete every server that was added with a cloud connection: click **Remove** in the **Nodes** table, or run `geass servers remove NAME`. Geass drains each node for up to five minutes, removes the node, and deletes its machine at the cloud. Wait until `geass nodes list` no longer shows them. Then look in the cloud's own console for machines that are left. These machines have the labels `geass-managed` and `geass-cluster`. If you uninstall K3s first, nothing can delete those machines anymore, and they continue to bill you.

Then, for servers that you joined over SSH, delete each node object from the control plane. Geass connects over SSH, runs `/usr/local/bin/k3s-agent-uninstall.sh`, and removes the node from the cluster:

```bash
sudo k3s kubectl -n geass-system get geassnodes
sudo k3s kubectl -n geass-system delete geassnode worker-1
```

Do this while the control plane still runs. If a server is unreachable, or the control plane is already gone, log in to the worker and run the script yourself:

```bash
sudo /usr/local/bin/k3s-agent-uninstall.sh
```

See [Add servers](/docs/getting-started/add-servers/#remove-a-node) for what removal does to the workloads on a node.

## Step 4: uninstall the control plane

On the control plane, run this command:

```bash
sudo /usr/local/bin/k3s-uninstall.sh
```

K3s's script does these things:

- It stops K3s.
- It unmounts and removes its mounts and network interfaces.
- It deletes `/etc/rancher/k3s`, its data directory, `/var/lib/kubelet`, the `k3s` binary, and its `kubectl`, `crictl`, and `ctr` links.

The embedded etcd database goes with the data directory, so you cannot recover the cluster after this step.

## Clean up the servers

If you want to remove what the installer and K3s left on each server, remove these items:

- **Longhorn data.** Run `sudo rm -rf /var/lib/longhorn`. This command deletes the only copies of database volumes on that node. Run it when you are sure that you no longer need them.
- **Firewall rules.** If `ufw` or `firewalld` is active, the installer opened 6443/tcp, 10250/tcp, and 8472/udp (51820/udp for a multi-site cluster). Delete these rules in your firewall, for example with `sudo ufw delete allow 6443/tcp`, or in your provider's security group. You must remove the rules that you added yourself, for example 80/tcp and 443/tcp.
- **A Tailscale mesh.** Tailscale stays installed, and each server stays on the tailnet, because K3s did not install Tailscale. Remove the servers in the Tailscale or Headscale admin console, or run `sudo tailscale logout` on each server. Revoke the auth key that you gave Geass. K3s's script deletes `/etc/rancher/k3s`, and with it the file that held the key (`vpn-auth`). The Secret `geass-mesh-auth` goes with the cluster.
- **iSCSI.** The `iscsid` service and the `open-iscsi` or `iscsi-initiator-utils` package are still installed.
- **Installer files.** `geass-install-*.log` in the directory that you ran the installer from, and the downloaded installer in your temporary directory.

## Clean up your own machines

When you remove the cluster, every API token is deleted. But the places that hold a token or call the cluster are on other machines.

- **Command line.** On each machine where you ran `geass login`, run `geass logout` to forget the token. Or delete the whole saved login: `config.json` in the `geass` folder of your user configuration directory. The path is one of these:
  - On Linux, `~/.config/geass/config.json`.
  - On macOS, `~/Library/Application Support/geass/config.json`.
  - On Windows, `%AppData%\geass\config.json`.
  - The path in `GEASS_CONFIG`.

  `geass logout` only forgets the token on that machine. To end a token while the cluster still runs, revoke it under **Settings → Tokens**. There, the tokens from `geass login` are named `CLI: geass CLI on <machine>`. To remove the program itself, delete the `geass` file that `install-cli.sh` put in `/usr/local/bin` or `~/.local/bin`.
- **CI and scripts.** Delete `GEASS_URL` and `GEASS_TOKEN` from the secret store of your CI system. Also delete the jobs that call `geass`.
- **AI agents.** Remove the `geass mcp` entry from each agent host. For example, run `claude mcp remove geass` in Claude Code, or remove the `geass` block in the configuration file of Claude Desktop. Stop any `geass mcp --listen` server.

## Check what is left in other accounts

| Where | Look for |
| --- | --- |
| AWS S3 | The S3 buckets of your own buckets, if a deletion did not finish. The backup bucket (default name `geass-backups-` plus 12 hex characters), and the objects in it under `<project-id>/<database-id>-<8 characters>/`. The last part comes from the database's UID |
| AWS IAM | Users whose names start with `geass-`: one for each bucket, and one for each backup bucket. Delete their access keys and the users |
| Every cloud that made machines | Machines labeled `geass-managed=true` (for OVHcloud, named `geass-...`), the firewalls that Geass made (`geass-<cluster>`), and the credentials that you gave a connection. Revoke these credentials. A security group or firewall rule that Geass added for the control plane has the description `geass` (AWS) or `geass-access` (Hetzner) |
| Cloudflare | DNS records for the dashboard and your services, the tunnel `geass`, and the API token that you gave Geass |
| GitHub | The GitHub App and its installations |
| PlanetScale | Databases and passwords that Geass created, and the service token |
| DNS provider | Any records that you created yourself |
| Your machines, CI and agent hosts | Saved `geass` logins, `GEASS_TOKEN` secrets, and `geass mcp` entries. See [Clean up your own machines](#clean-up-your-own-machines) |

## What happens under the hood

Resources are Kubernetes objects with finalizers: `geass.geass.dev/app`, `/database`, `/objectstore`, `/project`, and `/logicaldatabase`, and `/node` on nodes. When you delete a resource, Kubernetes marks it for deletion. The operator does its cleanup before it removes the finalizer. For this reason, the cleanup needs the operator to run, and the objects show **Deleting** until the cleanup finishes. To see what is waiting, run these commands:

```bash
sudo k3s kubectl -n geass-system get geassapps,geassdatabases,geassobjectstores,geasslogicaldatabases,geassprojects
sudo k3s kubectl -n geass-system logs deploy/geass-controller-manager -c manager
```

## Troubleshooting

**A resource stays in `Deleting`.** Look in the operator log for the error that the operator keeps retrying. Typical causes are these:

- An AWS connection that no longer exists.
- AWS credentials that expired or were revoked.
- For the in-cluster object storage server, project buckets that still use it.

Fix the cause, and the deletion finishes automatically. If you accept that the external resource stays behind, remove the finalizer by hand. This stops the cleanup permanently:

```bash
sudo k3s kubectl -n geass-system patch geassobjectstore <name> --type merge -p '{"metadata":{"finalizers":null}}'
```

**`k3s-uninstall.sh: No such file or directory`.** This machine has no K3s server. Workers have `k3s-agent-uninstall.sh`.

**A worker keeps appearing in the cluster after you deleted its node object.** Geass could not reach the machine, so its K3s agent still runs and registers again. Run the agent uninstall script on that machine.

**Disk space is not freed after the uninstall.** Longhorn's data directory is still there. See [Clean up the servers](#clean-up-the-servers).

## Related

- [Upgrade Geass](/docs/operations/upgrading/)
- [Command line](/docs/guides/command-line/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Object storage](/docs/guides/object-storage/)
- [Cloud connectors](/docs/guides/connectors/)
- [Add servers](/docs/getting-started/add-servers/)
