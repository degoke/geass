---
title: "Uninstall Geass"
description: "Remove Geass from your servers: the order to follow, what happens to workloads, volumes and backups, and the cloud resources you must clean up yourself."
---

Geass has no uninstall command. You remove it by deleting your resources from the dashboard, so that Geass cleans up what it created in other places, then removing workers and uninstalling K3s with K3s's own scripts. This page gives that order and says, from what the code does, what is deleted and what is left behind. The important part: Geass cleans up some external resources only when you delete the matching resource first, and it never removes others, such as DNS records and the backup bucket.

## What happens to what

| Thing | When K3s is uninstalled | Notes |
| --- | --- | --- |
| The operator, the dashboard, every Geass object, secrets, dashboard users, API tokens | Deleted | They live in the cluster's database, which K3s's uninstall script removes with its data directory |
| Workloads and their namespaces | Deleted | |
| The add-ons and what the operator created for them: KEDA and its HTTP add-on (the `keda` namespace), the operator's `ServiceMonitor` and `PrometheusRule`, the admission policy for compliance tags, and the `CronJob` objects of function schedules | Deleted | They are objects in the cluster's database. Geass itself never uninstalls KEDA or the admission policy, so only the cluster's removal takes them away |
| Volumes on K3s's `local-path` storage (metrics, log history, the in-cluster MinIO server, and databases created without Longhorn) | Deleted | They are under K3s's data directory (`/var/lib/rancher/k3s` by default) |
| Longhorn replica data (database volumes) | Left on disk | In Longhorn's data directory (`/var/lib/longhorn/` by default) on each node. Nothing refers to it any more. Remove it yourself |
| iSCSI initiator packages the installer added | Left | |
| Firewall rules the installer added | Left | |
| S3 buckets and IAM users of your **buckets** | Left, unless you deleted the bucket in Geass first | See [Buckets](#what-deleting-each-resource-does) |
| The S3 bucket and IAM user for **backups**, and the backups in it | Left | Geass never deletes the backup bucket or its user. It deletes a deleted database's backups after 7 days, but only while the operator runs |
| Cloudflare DNS records and the Cloudflare Tunnel | Left | Geass has no code that deletes them |
| The GitHub App | Left | Delete it in GitHub |
| PlanetScale databases and passwords | Left | |
| Worker servers | K3s agents keep running | Remove workers first, or run the agent uninstall script on each |
| Images your builds pushed to a registry | Left | |
| The `geass` command line's saved logins on your machines, the tokens in your CI secrets, and the `geass mcp` entries in AI agent hosts | Left | They hold tokens that the deleted cluster no longer recognizes. See [Clean up your own machines](#clean-up-your-own-machines) |

## Before you uninstall

- Dump any data you want to keep, with the databases' own tools, and copy the contents of in-cluster buckets. See [Upgrade Geass](/docs/operations/upgrading/) on why backups alone are not enough to rebuild: the dashboard restores from records that live in the cluster you are about to delete.
- Decide which external resources to keep: S3 buckets, DNS names, PlanetScale databases.
- Run the cleanup in the next sections while the dashboard still works. After the operator is gone, nothing can delete a bucket or an IAM user for you.

## Step 1: delete resources from the dashboard

Delete resources one kind at a time, and wait until each has disappeared before you go on. Each resource shows **Deleting** until its cleanup finishes.

1. **Services.** Open the service, go to **Delete service** in its **Settings**, type the service ID, choose **Delete service** and confirm with **Delete permanently**.
2. **Databases.** The same section, **Delete database**. If backups were on, Geass first takes a last backup (it waits up to 10 minutes for it), then keeps the backups restorable for 7 days. Without backups the data is destroyed.
3. **Buckets.** **Delete bucket**.
4. **Projects.** In the project's **Settings → Danger**, type the project name under **Delete Project**, choose **Delete** and confirm. The same page has **Manage Services**, which lists every resource of the project with a **Remove** button, so you can delete them one by one from there first.

:::caution
Delete buckets and databases before you delete their project, and wait for them to finish. Deleting a project also deletes the project's cloud connections (AWS and PlanetScale) in the same pass. The cleanup of an S3 bucket needs the AWS connection to delete the bucket and its IAM user, and the purge of an old database's backups needs it too. A bucket whose connection is already gone cannot be cleaned up: its object stays in `Deleting`, retrying with the error `AWS connection is unavailable`, and the S3 bucket and IAM user remain. This is how the code reads, and the order above avoids it.
:::

Deleting a project does not delete the credentials Secrets of its connections. A connection named `prod-aws` keeps a Secret named `prod-aws-credentials` in `geass-system`, with the access key inside. Delete it yourself and revoke the key in AWS.

### What deleting each resource does

| Resource | What Geass removes | What it leaves |
| --- | --- | --- |
| Service | The Deployment, Service, Ingress, autoscaler, metrics monitor and variables it created, its deployment history and its `<service-id>-secrets` Secret. For a service that sleeps when idle, its `<service-id>-wake` Service and `HTTPScaledObject`. For a function, its schedules (`CronJob` objects) and its `<service-id>-trigger` Secret | Images that builds pushed to a registry. Cloudflare DNS records for its hostname |
| In-cluster database | The database server, its Service and connection Secrets. PostgreSQL and SQLite volumes go with it (Longhorn volumes use a `Delete` reclaim policy). Redis keeps no volume | Backups in S3, for 7 days, then they are purged while the operator runs. MySQL's volume claims: Geass does not delete them, so check `kubectl get pvc -n <project-id>-<environment>`; they go when the project's namespace is deleted |
| External database (PlanetScale, AWS RDS) | Geass's own connection details | The database itself. Geass has no code to delete a PlanetScale or RDS database |
| Logical database on a shared server | The database and its login on the shared server, within 3 minutes | |
| In-cluster bucket (MinIO) | The buckets and the bucket's MinIO user | |
| AWS S3 bucket created by Geass | The bucket with every object, version and unfinished upload, and the scoped IAM user with its access keys and policy | |
| AWS S3 bucket attached from your account | The scoped IAM user with its keys and policy | The bucket and its contents |
| Project | All of the above for the project, the project's namespaces and everything in them, its shared-variable and webhook secrets, its GitHub link, its API tokens and its cloud connections | The connection credentials Secrets. External resources whose cleanup needed a connection |

A bucket or a database that was only a draft and never deployed has nothing outside the cluster to remove.

The in-cluster MinIO server (**Settings → Object storage**) cannot be deleted while project buckets exist on it. A shared database server (**Settings → Databases**) cannot be deleted while databases are on it.

## Step 2: disconnect connectors

In **Settings → Connectors**:

- **Cloudflare → Disconnect.** Type `remove-cloudflare`. This stops the `cloudflared` deployment and automatic DNS updates. Your Cloudflare account, the tunnel named `geass` and every DNS record Geass created stay. Delete them in Cloudflare: the proxied `A` record for the dashboard host (`geass.<your domain>` by default), the `CNAME` records pointing at the tunnel (dashboard and service hostnames, in Tunnel mode), and the tunnel.
- **GitHub → Remove GitHub App.** Type `remove-github`. This removes Geass's copy of the app's credentials. The GitHub App itself and its installations stay: delete them in your GitHub settings.
- **AWS and PlanetScale.** Connections are removed with their project (see above). Revoke the access keys and tokens at the provider.

## Step 3: remove workers

Delete each node object from the control plane. Geass connects over SSH, runs `/usr/local/bin/k3s-agent-uninstall.sh` and removes the node from the cluster:

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

On the control plane:

```bash
sudo /usr/local/bin/k3s-uninstall.sh
```

K3s's script stops K3s, unmounts and removes its mounts and network interfaces, and deletes `/etc/rancher/k3s`, its data directory, `/var/lib/kubelet` and the `k3s` binary and its `kubectl`, `crictl` and `ctr` links. The embedded etcd database goes with the data directory, so the cluster cannot be recovered afterwards.

## Clean up the servers

Remove what the installer and K3s left on each server, if you want it gone:

- **Longhorn data.** `sudo rm -rf /var/lib/longhorn`. This deletes the only copies of database volumes on that node. Run it when you are sure you no longer need them.
- **Firewall rules.** With `ufw` or `firewalld` active, the installer opened 6443/tcp, 10250/tcp and 8472/udp (51820/udp for a multi-site cluster). Delete them in your firewall, for example `sudo ufw delete allow 6443/tcp`, or in your provider's security group. Rules you added yourself, such as 80/tcp and 443/tcp, are yours to remove.
- **iSCSI.** The `iscsid` service and the `open-iscsi` or `iscsi-initiator-utils` package are still installed.
- **Installer files.** `geass-install-*.log` in the directory you ran the installer from, and the downloaded installer in your temporary directory.

## Clean up your own machines

The cluster's removal deletes every API token, but the places that hold a token or call the cluster are on other machines.

- **Command line.** On each machine where you ran `geass login`, run `geass logout` to forget the token, or delete the whole saved login: `config.json` in the `geass` folder of your user configuration directory (on Linux `~/.config/geass/config.json`, on macOS `~/Library/Application Support/geass/config.json`, on Windows `%AppData%\geass\config.json`, or the path in `GEASS_CONFIG`). `geass logout` only forgets the token on that machine. To end a token while the cluster still runs, revoke it under **Settings → Tokens**, where the ones from `geass login` are named `CLI: geass CLI on <machine>`. To remove the program itself, delete the `geass` file that `install-cli.sh` put in `/usr/local/bin` or `~/.local/bin`.
- **CI and scripts.** Delete `GEASS_URL` and `GEASS_TOKEN` from your CI system's secret store, and the jobs that call `geass`.
- **AI agents.** Remove the `geass mcp` entry from each agent host, for example `claude mcp remove geass` in Claude Code, or the `geass` block in Claude Desktop's configuration file. Stop any `geass mcp --listen` server.

## Check what is left in other accounts

| Where | Look for |
| --- | --- |
| AWS S3 | Buckets of your own buckets, if a deletion did not finish. The backup bucket (default name `geass-backups-` plus 12 hex characters), and the objects in it under `<project-id>/<database-id>-<8 characters>/`, where the last part comes from the database's UID |
| AWS IAM | Users whose names start with `geass-`: one per bucket, and one per backup bucket. Delete their access keys and the users |
| Cloudflare | DNS records for the dashboard and your services, the tunnel `geass`, and the API token you gave Geass |
| GitHub | The GitHub App and its installations |
| PlanetScale | Databases and passwords created through Geass, and the service token |
| DNS provider | Any records you created yourself |
| Your machines, CI and agent hosts | Saved `geass` logins, `GEASS_TOKEN` secrets and `geass mcp` entries. See [Clean up your own machines](#clean-up-your-own-machines) |

## What happens under the hood

Resources are Kubernetes objects with finalizers: `geass.geass.dev/app`, `/database`, `/objectstore`, `/project` and `/logicaldatabase`, and `/node` on nodes. Deleting one marks it for deletion, and the operator does its cleanup before it removes the finalizer. That is why the cleanup depends on the operator running, and why the objects show **Deleting** in the meantime. See what is waiting:

```bash
sudo k3s kubectl -n geass-system get geassapps,geassdatabases,geassobjectstores,geasslogicaldatabases,geassprojects
sudo k3s kubectl -n geass-system logs deploy/geass-controller-manager -c manager
```

## Troubleshooting

**A resource stays in `Deleting`.** Look in the operator log for the error it keeps retrying. Typical causes are an AWS connection that no longer exists, expired or revoked AWS credentials, or, for an in-cluster MinIO server, project buckets that still use it. Fix the cause and it finishes by itself. If you accept that the external resource will be left behind, remove the finalizer by hand. This stops the cleanup for good:

```bash
sudo k3s kubectl -n geass-system patch geassobjectstore <name> --type merge -p '{"metadata":{"finalizers":null}}'
```

**`k3s-uninstall.sh: No such file or directory`.** This machine has no K3s server. Workers have `k3s-agent-uninstall.sh`.

**A worker keeps appearing in the cluster after you deleted its node object.** Geass could not reach the machine, so its K3s agent still runs and registers again. Run the agent uninstall script on it.

**Disk space is not freed after the uninstall.** Longhorn's data directory is still there. See [Clean up the servers](#clean-up-the-servers).

## Related

- [Upgrade Geass](/docs/operations/upgrading/)
- [Command line](/docs/guides/command-line/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Object storage](/docs/guides/object-storage/)
- [Cloud connectors](/docs/guides/connectors/)
- [Add servers](/docs/getting-started/add-servers/)
