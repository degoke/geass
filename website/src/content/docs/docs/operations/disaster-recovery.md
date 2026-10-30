---
title: "Disaster recovery"
description: "What survives the loss of a node, a disk, or the control plane. Keep copies of the cluster state in your own S3 bucket, and rebuild from them."
---

Geass protects your data in three layers, and each layer protects against different problems. This page gives this information:

- The loss that each layer covers.
- How to turn on the layer that is not on by default. That layer keeps copies of the state of the cluster in a bucket that you own.
- How to rebuild a cluster from those copies when the disk of the cluster is gone.

| Layer | Protects against | On by default? |
| --- | --- | --- |
| **Copies inside the cluster**: replicated volumes, highly available databases, three control plane servers | The loss of a machine | Longhorn replicates volumes when Longhorn is on. Databases and servers are [your choice](/docs/operations/high-availability/) |
| **Database backups** in your S3 bucket | The loss of the data of a database, or of the cluster that it ran in | On for new Postgres databases. See [Backups and restore](/docs/guides/backups-and-restore/) |
| **Copies of the cluster's own state** in your S3 bucket | The loss of the disk of the control plane. That disk holds every project, service, secret, and record of your backups | **No: you opt in.** This page |

Copies inside the cluster do not help against a mistake, or against a lost disk that every copy shares. Also, a backup bucket has no use if nothing knows what is in it. The third layer lets you find the contents of the second layer again.

## What a loss costs

| What is lost | What happens | What brings it back |
| --- | --- | --- |
| A worker machine | Its services restart on other nodes after five minutes, and a highly available database fails over. After [15 minutes](/docs/operations/high-availability/#what-geass-does-after-fifteen-minutes), a database with one instance starts on another node, and Geass replaces a lost instance of a highly available Postgres database. See [When a node is lost](/docs/operations/high-availability/#when-a-node-is-lost) | The node comes back, or you add a new machine with [Add servers](/docs/getting-started/add-servers/) |
| One of three control plane servers | Nothing stops. If the operator was on that server, the operator moves in one to two minutes | [Take a server away](/docs/operations/high-availability/#take-a-server-away), and join a new one |
| The only control plane server, with its disk | Containers that run on workers continue to run, but you cannot change or restart anything. Everything that Geass keeps is on that disk: projects, services, secrets, tokens, and the records of your backups | Copies of the cluster's state in S3: [Rebuild a cluster from a copy](#rebuild-a-cluster-from-a-copy). Without them: [Recover databases from a bucket](#recover-databases-from-a-bucket) |
| The disk of a worker, or the part of that disk that the volume of a database was on | Volumes with a copy on another node continue. Databases on local storage lose their data | Restore from the [backup](/docs/guides/backups-and-restore/) of the database into a new database |

:::caution
Keep the copies that this page describes. A new install is one control plane server, and its disk holds the only copy of the cluster. K3s takes snapshots of its own database twice a day, but it keeps them on that same disk. Without the copies on this page, if you lose the disk, you lose the cluster.
:::

## Keep copies of the cluster's state

K3s can send each of its snapshots to an S3 bucket, and also keep them on disk. Geass sets this up in two parts:

1. You tell K3s on the control plane one time to send copies. This part needs the installer, because it changes the machine and restarts K3s.
2. The cluster gives the location of the bucket. This part is a setting that you can change at any time, with no restart.

**What a copy holds.** A copy holds everything that the cluster stores:

- The custom resources.
- Every Secret, including your cloud keys, SSH keys, and database passwords. The Secrets are in plain text, unless the cluster encrypts its Secrets at rest.

A copy does not hold the contents of your databases, your volumes, or your buckets. Treat the bucket as if it holds every secret that you have. Even a copy of an encrypted cluster holds the keys to that cluster, and only the server token protects them.

**What it costs.** A copy of a small cluster is a few megabytes. K3s takes a copy at 00:00 and 12:00 server time. It keeps 14 copies, that is one week, on the machine and in the bucket. If you have three control plane servers, each server takes a copy, and the servers share the 14 copies in the bucket.

### 1. Make the bucket and a key for it

Make a bucket for the copies only, with encryption on and public access blocked. Make a user whose key can use that bucket and nothing else. To do this with the AWS CLI, run these commands:

```bash
aws s3api create-bucket --bucket acme-geass-cluster-state --region eu-north-1 \
  --create-bucket-configuration LocationConstraint=eu-north-1
aws s3api put-public-access-block --bucket acme-geass-cluster-state \
  --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true
aws s3api put-bucket-encryption --bucket acme-geass-cluster-state \
  --server-side-encryption-configuration '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}'
aws iam create-user --user-name geass-cluster-state
aws iam put-user-policy --user-name geass-cluster-state --policy-name cluster-state --policy-document file://policy.json
aws iam create-access-key --user-name geass-cluster-state
```

`policy.json` has this content, with the name of your bucket. The Usage page shows this policy with the name of your bucket in it:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:ListBucket", "s3:GetBucketLocation", "s3:ListBucketMultipartUploads"],
      "Resource": ["arn:aws:s3:::acme-geass-cluster-state"]
    },
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:AbortMultipartUpload", "s3:ListMultipartUploadParts"],
      "Resource": ["arn:aws:s3:::acme-geass-cluster-state/*"]
    }
  ]
}
```

The key can list the bucket, and it can read, write, and delete the copies in the bucket. The key cannot make or delete a bucket, change the settings of a bucket, or touch any other bucket. Geass tested this policy against a server that enforces it, with a copy large enough that the upload used parts.

You can also use a bucket on another S3-compatible server. Geass tested MinIO. When you set up the bucket, enter its address in **Server**, with no `https://`.

### 2. Tell K3s to send copies

When you [install Geass](/docs/getting-started/install/), the installer asks on the terminal whether to keep copies of the cluster in your own S3 bucket. The answer is **no** unless you say yes. You can give the answer and the bucket before the installer asks. Then you have nothing left to do after the install:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | \
  sudo env GEASS_ETCD_SNAPSHOTS_BUCKET=acme-geass-cluster-state GEASS_ETCD_SNAPSHOTS_REGION=eu-north-1 \
  GEASS_ETCD_SNAPSHOTS_ACCESS_KEY=AKIA… GEASS_ETCD_SNAPSHOTS_SECRET_KEY=… sh -s -- --etcd-snapshots
```

On a cluster that is already running, run the `etcd-snapshots` command of the installer on the control plane server. The command restarts K3s. The API server is unavailable for about one minute, but the workloads that run do not stop.

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | \
  sudo env GEASS_ETCD_SNAPSHOTS_BUCKET=acme-geass-cluster-state GEASS_ETCD_SNAPSHOTS_REGION=eu-north-1 \
  GEASS_ETCD_SNAPSHOTS_ACCESS_KEY=AKIA… GEASS_ETCD_SNAPSHOTS_SECRET_KEY=… sh -s -- etcd-snapshots enable
```

Without the bucket variables, the command does only the first part, and tells you where to give the bucket. These are the variables:

- `GEASS_ETCD_SNAPSHOTS_BUCKET`.
- `_REGION`.
- `_ENDPOINT`, for a server that is not AWS, with no scheme.
- `_FOLDER`. The default is `geass`.
- `_INSECURE`, for plain HTTP on a network that is closed to everyone else.
- The pair `_ACCESS_KEY` and `_SECRET_KEY`.

The installer checks the variables before it changes anything. Then it waits for the operator to write the settings, and it takes a first copy immediately. Thus you know immediately that the copies work, and you do not find a problem at noon.

The installer has two more commands:

- `etcd-snapshots status` tells you whether this server sends copies, and what the bucket holds.
- `etcd-snapshots disable` stops the copies from this server. The copies that are already in the bucket stay.

In a cluster with three control plane servers, each server takes snapshots. Thus run `etcd-snapshots enable` on every server, one server at a time. For a server that joins, add `--etcd-snapshots` to `--join-server`. The installer needs K3s v1.30.3 or newer for this. If your K3s is older, the installer tells you.

### 3. Keep the server token somewhere else

K3s encrypts the keys of the cluster inside every copy with the **server token** of the cluster. You cannot restore a copy without the token. The token is a file on the same disk that the copies protect. When you turn copies on, the installer shows the token on the terminal and asks you to keep it. Keep it at that time, in a password manager. The token is also in `/var/lib/rancher/k3s/server/token`.

Also write down these items:

- The K3s release that the cluster runs. The **Usage** page shows it, and `k3s --version` also shows it.
- The address of the control plane server.

### Set or change the bucket from the dashboard

Open **Usage**, and select **Cluster backup**. The section shows whether copies arrive, the age of the newest copy, and the number of copies in the bucket. **Set up** and **Change bucket** take the bucket and its key. Geass keeps the key in a Secret and never shows it again. A change takes effect with no restart. K3s reads the location for the copies from a Secret that Geass keeps up to date.

On the cluster object, the same setting is:

```yaml
spec:
  etcdSnapshots:
    bucket: acme-geass-cluster-state
    region: eu-north-1
    folder: geass                       # optional
    endpoint: minio.example.com:9000    # optional; leave out for AWS
    credentialsSecretRef:               # optional; geass-etcd-snapshot-credentials by default
      name: cluster-state-key           # a Secret in geass-system with ACCESS_KEY_ID and ACCESS_SECRET_KEY
```

### How you are told it stopped

The **Cluster backup** state of the cluster is one of these:

| State | Means |
| --- | --- |
| **Waiting for the first copy** | The bucket is set, and no copy has arrived yet. K3s sends a copy at 00:00 and 12:00 server time. To take a copy now, run `sudo k3s etcd-snapshot save` on the control plane |
| **Copies are arriving** | The newest copy in the bucket is less than 25 hours old |
| **Copies are not arriving** | One of these: no copy came within 13 hours after you set the bucket, the newest copy is more than 25 hours old, Geass could not read the bucket with the key, or the key is not in the cluster. The message tells you which |

When copies stop arriving, Geass sends a **Cluster problem** [alert](/docs/guides/alerts/) after ten minutes. If a bucket stays empty, the usual reason is that nobody told K3s to send copies. Run `etcd-snapshots enable` on the control plane.

## Rebuild a cluster from a copy

You need these items:

- The bucket and its key.
- The **server token** of the cluster.
- The K3s release that the cluster ran.
- A machine that meets the [requirements](/docs/getting-started/requirements/).

1. **Use the same address if you can.** Workers find the control plane at the address that they got when they joined. The new machine can take the address of the old machine, for example an elastic IP, a DNS name, or the old private IP. Then the workers reconnect without changes. If not, after the restore, point the K3s agent of each worker to the new address. Before step 2, put the same `/etc/rancher/k3s/config.yaml` on the new machine in these cases:

   - The cluster was built for [nodes at more than one provider](/docs/operations/multi-provider/).
   - You changed `/etc/rancher/k3s/config.yaml`.

2. **Install K3s, but do not start it.** Use the K3s release that the cluster ran. Install it as a server with its own etcd, as the installer does:

   ```bash
   curl -sfL https://get.k3s.io | sudo env INSTALL_K3S_VERSION=v1.36.5+k3s1 INSTALL_K3S_SKIP_START=true \
     INSTALL_K3S_EXEC="server --cluster-init" sh -
   ```

3. **Say where the copies are, in a file that is read once.** Do not put these settings on the command line, because the process list shows the key and the token:

   ```bash
   sudo mkdir -p /etc/rancher/k3s/config.yaml.d
   sudo install -m 600 /dev/stdin /etc/rancher/k3s/config.yaml.d/90-geass-restore.yaml <<'EOF'
   etcd-s3: true
   etcd-s3-bucket: "acme-geass-cluster-state"
   etcd-s3-folder: "geass"
   etcd-s3-region: "eu-north-1"
   etcd-s3-access-key: "AKIA…"
   etcd-s3-secret-key: "…"
   token: "K10…::server:…"
   EOF
   ```

   For a server that is not AWS, add `etcd-s3-endpoint`. For plain HTTP, also add `etcd-s3-insecure: true`. `token` is the **old** server token of the cluster.

   If the old cluster had `secrets-encryption: true` in its `/etc/rancher/k3s/config.yaml`, put that line in this file too. A restore without that line stops with `identity transformer tried to read encrypted data`. K3s refuses settings that are different from the settings of the cluster. Thus add here every other setting that you set in that file on the old machine.

4. **Pick the copy.** Usually, the newest copy is correct. List the copies in the bucket, with the newest last:

   ```bash
   aws s3 ls s3://acme-geass-cluster-state/geass/
   ```

   The name is the part after the folder, for example `etcd-snapshot-cp1-1792389364`. On the new machine, with the settings of step 3 in place, `sudo k3s etcd-snapshot ls` also lists the copies from the bucket. If you can still read the old cluster, `kubectl -n geass-system get geasscluster default -o jsonpath='{.status.etcdSnapshots.lastCopyName}'` gives the newest copy that the old cluster saw.

5. **Restore it.** This command is the restore of K3s itself. The K3s documentation gives it as the way to restore a snapshot onto a new machine:

   ```bash
   sudo k3s server --cluster-init --cluster-reset --cluster-reset-restore-path=etcd-snapshot-cp1-1792389364
   ```

   The command ends with a line that says `restart without --cluster-reset flag now`. That line is the usual end, not an error. If the command stops with `bootstrap data already found and encrypted with different token`, the token in step 3 is not the token that the cluster had. You cannot read a copy with a different token. Find the correct token before you try again.

   Do not change the address of the machine between this step and the next step. K3s records that address as the address of the one member of the cluster.

6. **Remove the restore file and start K3s.** The key and the token must not stay in a file:

   ```bash
   sudo rm /etc/rancher/k3s/config.yaml.d/90-geass-restore.yaml
   sudo systemctl start k3s
   sudo k3s kubectl get nodes
   ```

   The cluster is back as it was at the time of the copy. The list of nodes still shows the old control plane server, as `NotReady`. Remove it with `sudo k3s kubectl delete node <old name>`. If the new machine has the same name, do not remove it, because it is the same node.

7. **Put the operator on the machine, and send copies again.** The image of the operator is inside the installer, not in a registry. Thus run the installer of the release that the cluster ran. On a machine that has K3s with Geass in it, the installer updates the operator and keeps everything:

   ```bash
   curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo env GEASS_VERSION=<the release> sh
   curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh -s -- etcd-snapshots enable
   ```

   The installer checks the cluster before it changes anything. The installer **refuses while Longhorn volumes are missing copies**. A rebuild leaves the volumes in this state, because the copies that were on the lost machine are gone. Read the list that the installer shows. If the list shows only those volumes, run the first command again with `sh -s -- --force` in place of `sh`.

   The bucket and its key came back inside the copy. Thus the second command needs none of the variables. It only tells K3s on this machine to send copies. That is configuration of the machine, not of the cluster. The command says that the cluster already has its bucket, and it takes a copy immediately.

8. **Tell Longhorn about the new disk.** If the cluster does not run Longhorn, skip this step. Longhorn identifies the disk of each node by an identifier that was in the copy. The disk of the new machine has a different identifier. Thus Longhorn does these things:

   - It marks that disk `DiskFilesystemChanged`.
   - It puts no copies on that disk.
   - Replicated volumes stay one copy short.

   Look at the node:

   ```bash
   kubectl -n longhorn-system get nodes.longhorn.io <node name> -o jsonpath='{.spec.disks}{"\n"}{.status.diskStatus}'
   ```

   The `Schedulable` condition of the disk is `False` with the reason `DiskNotReady`. Remove the disk and add it again. Longhorn removes a disk only after the disk is disabled. The new entry needs a name that is different from the old name. Use the name that `.spec.disks` shows, for example `default-disk-7f80b132923582ce`. Use the fields that the next commands show for the new entry:

   ```bash
   kubectl -n longhorn-system patch nodes.longhorn.io <node name> --type merge -p '{"spec":{"disks":{"<disk name>":{"allowScheduling":false}}}}'
   kubectl -n longhorn-system patch nodes.longhorn.io <node name> --type merge -p '{"spec":{"disks":{"<disk name>":null}}}'
   kubectl -n longhorn-system patch nodes.longhorn.io <node name> --type merge -p '{"spec":{"disks":{"default-disk":{"allowScheduling":true,"diskType":"filesystem","path":"/var/lib/longhorn/","tags":[]}}}}'
   ```

   The disk is schedulable again within one minute. The volumes that were one copy short build the missing copy on that disk.

   An instance of a highly available Postgres database that had its volume on the lost machine lost its data with that machine. If that instance cannot start for [15 minutes](/docs/operations/high-availability/#what-geass-does-after-fifteen-minutes), Geass replaces it automatically. Then the Postgres operator builds a new instance from the primary.

9. **Check what came back.** Open the dashboard. Projects, services, databases, domains, and tokens are as they were. Some workers can reach the control plane at the address that they joined with, with the token that they joined with. These workers reconnect automatically, because K3s agents do this. If a worker cannot reconnect, point its agent to the new address. Everything that ran on the lost machine itself is gone, and so are the volumes that were on its disk.

The rebuild does not bring back these items:

- Each change after Geass took the copy. That is a maximum of 12 hours.
- The contents of local volumes that were on the lost machine.
- Everything outside the cluster, for example DNS records, S3 buckets, and your cloud accounts.

## Recover databases from a bucket

You can still find the database backups in these cases:

- The copies of the cluster are also lost.
- You move to a new cluster, and you have only the bucket that holds your database backups.

The backups of each database are in a folder with the name of its project. **Beside them Geass keeps a small record of what they are copies of**: the engine, the version, the size, and the name of the database. The record holds nothing secret. An archive writes the record a few minutes after a cluster runs this release. Backups from before that time have no record until the cluster that made them runs this release.

To recover the databases, do these steps:

1. On the new cluster, open **Usage**, and select **Recover databases from a bucket**.
2. Enter the bucket, its region, and the AWS key that Geass used for backups. Geass uses that key to make a restricted user for the bucket, as it does for every project.

Geass then does these things:

- It finds the record of each database, and finds the restore points of the database from the contents of the bucket.
- It makes a project again for each project that this cluster does not have. The project gets the name that the backups use, and a cloud connection to the bucket.
- It makes a record of the backups of each database. That record then appears in **Settings**, **Backups** of that project, and you restore it as usual.

Geass changes nothing in the bucket and starts nothing. It lists what it left out, and why. Geass leaves out backups from before records were written, and backups whose record is from a newer release than yours.

Geass offers the records that it makes for 30 days. **When they expire, only the records go: Geass never deletes the data in a bucket it found,** because the cluster that made the data might still be alive.

## Disk failures

When a disk fails, the cluster tells you with the **Storage failing** [alert](/docs/guides/alerts/):

| Sent when | Means | After |
| --- | --- | --- |
| The disk of a node has become **read-only** | The system does this to a disk that fails, or to a filesystem that is corrupted. Everything that writes to it fails | 2 minutes |
| A volume has **lost a copy** and has not got it back | The volume still works, but one more loss loses the data | 30 minutes |
| A volume has **lost every copy** | Everything that uses the volume is down | 5 minutes |

A disk can fail without any of these signs. For example, if nothing has read a few bad sectors yet, nothing that Geass watches sees them. Replicated storage and backups protect against this failure. Detection does not.

## Limits

- A copy of the cluster holds its objects, not the contents of databases, volumes, or in-cluster buckets. Databases need their own [backups](/docs/guides/backups-and-restore/).
- Redis has no backup, and Geass does not back up the object storage server of the cluster.
- Geass replaces an instance of a highly available Postgres database when its node was lost, or when its volume has no copy left. Sometimes the volume of an instance is healthy but empty, because somebody removed its data from a disk that is still there. That instance continues to crash. Longhorn makes a new empty copy, and nothing can tell that instance from a database that is slow to start. To replace that instance by hand, remove its claim, and then remove its pod.
- Geass makes a new machine only for a worker that it created at a cloud, and only in the cases that [Node pools](/docs/guides/node-pools/#safety) lists. For a control plane server, and for a server that you joined over SSH, the replacement starts when you add the new machine.
- The copies are as old as the last 00:00 or 12:00. Before a risky change, take a copy with `sudo k3s etcd-snapshot save`.
- A cluster that Geass did not install cannot send copies this way. A cluster that uses a database other than the etcd of K3s also cannot.

## Related

- [High availability](/docs/operations/high-availability/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Upgrade Geass](/docs/operations/upgrading/#go-back)
- [Alerts](/docs/guides/alerts/)
- [Storage and volumes](/docs/guides/storage/)
