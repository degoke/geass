---
title: "Disaster recovery"
description: "What survives losing a node, a disk or the control plane, how to keep copies of the cluster's state in your own S3 bucket, and how to rebuild from them."
---

Geass protects your data in three layers, and they protect against different things. This page says which loss each one covers, how to turn on the one that is not on by default (copies of the cluster's own state, in a bucket of yours), and how to rebuild a cluster from it when its disk is gone.

| Layer | Protects against | On by default? |
| --- | --- | --- |
| **Copies inside the cluster**: replicated volumes, highly available databases, three control plane servers | Losing a machine | Volumes are replicated when Longhorn is on. Databases and servers are [your choice](/docs/operations/high-availability/) |
| **Database backups** in your S3 bucket | Losing a database's data, or the cluster it ran in | On for new Postgres databases. See [Backups and restore](/docs/guides/backups-and-restore/) |
| **Copies of the cluster's own state** in your S3 bucket | Losing the disk of the control plane, which holds every project, service, secret and record of your backups | **No: you opt in.** This page |

Copies inside the cluster do not help against a mistake or a lost disk that every copy shares, and a backup bucket is no use if nothing knows what is in it. The third layer is what makes the second one findable again.

## What a loss costs

| What is lost | What happens | What brings it back |
| --- | --- | --- |
| A worker machine | Its services restart on other nodes after five minutes, and a highly available database fails over. A database with one instance starts on another node, and a lost instance of a highly available Postgres database is replaced, after [15 minutes](/docs/operations/high-availability/#what-geass-does-after-fifteen-minutes). See [When a node is lost](/docs/operations/high-availability/#when-a-node-is-lost) | The node returning, or a new machine added with [Add servers](/docs/getting-started/add-servers/) |
| One of three control plane servers | Nothing stops. The operator moves in one to two minutes if it was on that server | [Take a server away](/docs/operations/high-availability/#take-a-server-away), and join a new one |
| The only control plane server, with its disk | Running containers on workers keep running, and nothing can be changed or restarted. Everything Geass keeps (projects, services, secrets, tokens, the records of your backups) is on that disk | Copies of the cluster's state in S3: [Rebuild a cluster from a copy](#rebuild-a-cluster-from-a-copy). Without them: [Recover databases from a bucket](#recover-databases-from-a-bucket) |
| A worker's disk, or the part of it a database's volume was on | Volumes with a copy on another node carry on. Databases on local storage lose their data | Restore from the database's [backup](/docs/guides/backups-and-restore/) into a new database |

:::caution
A new install is one control plane server, and its disk holds the only copy of the cluster. K3s takes snapshots of its own database, twice a day, but it keeps them on that same disk. Without the copies on this page, losing the disk loses the cluster.
:::

## Keep copies of the cluster's state

K3s can send each of its snapshots to an S3 bucket as well as keeping them on disk. Geass sets that up in two parts: K3s on the control plane is told once to send copies, and the cluster says where. The first needs the installer, because it changes the machine and restarts K3s. The second is a setting you can change at any time, with no restart.

**What a copy holds.** Everything the cluster stores: the custom resources, and every Secret, including your cloud keys, SSH keys and database passwords, in plain text unless the cluster encrypts its Secrets at rest. It does not hold the contents of your databases, your volumes or your buckets. Treat the bucket as holding every secret you have: even a copy of an encrypted cluster holds the keys to it, protected only by the server token.

**What it costs.** A copy of a small cluster is a few megabytes. K3s takes one at 00:00 and 12:00 server time and keeps 14 of them, a week, on the machine and in the bucket. With three control plane servers, which each take one, the 14 in the bucket are shared between them.

### 1. Make the bucket and a key for it

Make a bucket for this alone, with encryption on and public access blocked, and a user whose key can use that bucket and nothing else. With the AWS CLI:

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

`policy.json` is this, with your bucket's name. The Usage page shows it with the name filled in:

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

The key can list the bucket and read, write and delete the copies in it. It cannot make or delete a bucket, change its settings, or touch any other. Geass tested that against a server that enforces the policy, with a copy large enough to be sent in parts.

A bucket on another S3-compatible server can be used too (Geass tested MinIO). Say its address in **Server**, with no `https://`, when you set it up.

### 2. Tell K3s to send copies

When you [install Geass](/docs/getting-started/install/), the installer asks on the terminal whether to keep copies of the cluster in your own S3 bucket. The answer is **no** unless you say yes. To say it ahead, and to give it the bucket in the same step, so that nothing is left to do afterwards:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | \
  sudo env GEASS_ETCD_SNAPSHOTS_BUCKET=acme-geass-cluster-state GEASS_ETCD_SNAPSHOTS_REGION=eu-north-1 \
  GEASS_ETCD_SNAPSHOTS_ACCESS_KEY=AKIA… GEASS_ETCD_SNAPSHOTS_SECRET_KEY=… sh -s -- --etcd-snapshots
```

On a cluster that is already running, turn it on with the installer's `etcd-snapshots` command, on the control plane server. It restarts K3s: the API server is away for about a minute, and what is running is not stopped.

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | \
  sudo env GEASS_ETCD_SNAPSHOTS_BUCKET=acme-geass-cluster-state GEASS_ETCD_SNAPSHOTS_REGION=eu-north-1 \
  GEASS_ETCD_SNAPSHOTS_ACCESS_KEY=AKIA… GEASS_ETCD_SNAPSHOTS_SECRET_KEY=… sh -s -- etcd-snapshots enable
```

Without the bucket variables it only does the first part, and says where to give the bucket. The variables are `GEASS_ETCD_SNAPSHOTS_BUCKET`, `_REGION`, `_ENDPOINT` (for a server that is not AWS, with no scheme), `_FOLDER` (`geass` unless you choose), `_INSECURE` (plain HTTP, for a network closed to everyone else) and the pair `_ACCESS_KEY` and `_SECRET_KEY`. The installer checks them before it changes anything. Then it waits for the operator to write the settings and takes a first copy at once, so that you know it works instead of finding out at noon.

Two more commands: `etcd-snapshots status` says whether this server sends copies and what the bucket holds, and `etcd-snapshots disable` stops this server sending them. Copies already in the bucket stay.

A cluster with three control plane servers needs this on each of them, because each takes snapshots: run `etcd-snapshots enable` on every server, one at a time. A server that joins can be told with `--etcd-snapshots` on `--join-server`. The installer needs K3s v1.30.3 or newer for this, and tells you if yours is older.

### 3. Keep the server token somewhere else

K3s encrypts the cluster's keys inside every copy with the cluster's **server token**. A copy cannot be restored without it, and the token is a file on the same disk that the copies are for. The installer shows it to you on the terminal when you turn copies on, and asks you to store it. Do it then, in a password manager. It is also in `/var/lib/rancher/k3s/server/token`.

Write down, too, the K3s release the cluster runs (the **Usage** page shows it, and so does `k3s --version`), and the address of the control plane server.

### Set or change the bucket from the dashboard

Open **Usage**, and **Cluster backup**. It says whether copies are arriving, how old the newest is, and how many the bucket holds. **Set up** and **Change bucket** take the bucket and its key, and the key is stored in a Secret and never shown again. A change takes effect with no restart: K3s reads where to send copies from a Secret that Geass keeps up to date.

On the cluster object the same is:

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

The cluster's **Cluster backup** state is one of:

| State | Means |
| --- | --- |
| **Waiting for the first copy** | The bucket is set and nothing has arrived yet. K3s sends one at 00:00 and 12:00 server time. To take one now, run `sudo k3s etcd-snapshot save` on the control plane |
| **Copies are arriving** | The newest copy in the bucket is less than 25 hours old |
| **Copies are not arriving** | No copy came within 13 hours of the bucket being set, the newest is more than 25 hours old, the bucket could not be read with the key, or the key is not in the cluster. The message says which |

A copy that stops arriving is a **Cluster problem** [alert](/docs/guides/alerts/) after ten minutes. The usual reason for a bucket that stays empty is that K3s was never told to send copies: run `etcd-snapshots enable` on the control plane.

## Rebuild a cluster from a copy

You need: the bucket and its key, the cluster's **server token**, the K3s release the cluster ran, and a machine that meets the [requirements](/docs/getting-started/requirements/).

1. **Use the same address if you can.** Workers find the control plane at the address they were given when they joined. A new machine that takes over the old one's address (an elastic IP, a DNS name, or the old private IP) lets them reconnect without changes. Otherwise point each worker's K3s agent at the new address after the restore. If the cluster was built for [nodes at more than one provider](/docs/operations/multi-provider/), or you changed `/etc/rancher/k3s/config.yaml`, put the same file on the new machine before step 2.

2. **Install K3s, but do not start it.** Use the K3s release the cluster ran, as a server with its own etcd, as the installer does:

   ```bash
   curl -sfL https://get.k3s.io | sudo env INSTALL_K3S_VERSION=v1.36.5+k3s1 INSTALL_K3S_SKIP_START=true \
     INSTALL_K3S_EXEC="server --cluster-init" sh -
   ```

3. **Say where the copies are, in a file that is read once.** Not on the command line, where the process list shows the key and the token:

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

   For a server that is not AWS add `etcd-s3-endpoint` (and `etcd-s3-insecure: true` for plain HTTP). `token` is the cluster's **old** server token. If the old cluster had `secrets-encryption: true` in its `/etc/rancher/k3s/config.yaml`, put that line in this file too: a restore without it stops with `identity transformer tried to read encrypted data`. K3s refuses settings that differ from the cluster's, so anything else you set in that file on the old machine goes here as well.

4. **Pick the copy.** The newest one is usually right. List them in the bucket, newest last:

   ```bash
   aws s3 ls s3://acme-geass-cluster-state/geass/
   ```

   The name is what follows the folder, such as `etcd-snapshot-cp1-1792389364`. On the new machine, with the settings of step 3 in place, `sudo k3s etcd-snapshot ls` lists them from the bucket too. If you can still read the old cluster, `kubectl -n geass-system get geasscluster default -o jsonpath='{.status.etcdSnapshots.lastCopyName}'` says the newest it saw.

5. **Restore it.** This is K3s's own restore, documented by K3s as the way to restore a snapshot onto a new machine:

   ```bash
   sudo k3s server --cluster-init --cluster-reset --cluster-reset-restore-path=etcd-snapshot-cp1-1792389364
   ```

   It ends with a line that says `restart without --cluster-reset flag now`. That is the normal end, not an error. If it stops instead with `bootstrap data already found and encrypted with different token`, the token in step 3 is not the one the cluster had: a copy cannot be read with another, so find the right one before you try again. Do not change the machine's address between this step and the next: K3s records it as the address of the cluster's one member.

6. **Take the restore file away and start K3s.** The key and the token do not stay in a file:

   ```bash
   sudo rm /etc/rancher/k3s/config.yaml.d/90-geass-restore.yaml
   sudo systemctl start k3s
   sudo k3s kubectl get nodes
   ```

   The cluster is back as it was at the time of the copy. The old control plane server is still listed as a node, `NotReady`: remove it with `sudo k3s kubectl delete node <old name>` unless the new machine has the same name, in which case it is the same node.

7. **Put the operator on the machine, and send copies again.** The operator's image is inside the installer, not in a registry, so run the installer of the release the cluster ran. On a machine that has K3s with Geass in it, it updates the operator and keeps everything:

   ```bash
   curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo env GEASS_VERSION=<the release> sh
   curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh -s -- etcd-snapshots enable
   ```

   The installer checks the cluster before it changes anything, and **refuses while Longhorn volumes are missing copies**, which is what a rebuild leaves: the copies that were on the lost machine are gone. Look at what it lists, and when it is only that, run the first command again with `sh -s -- --force` in place of `sh`. The bucket and its key came back inside the copy, so the second command needs none of the variables: it only tells K3s on this machine to send copies, which is configuration of the machine and not of the cluster. It says that the cluster already has its bucket, and takes a copy at once.

8. **Tell Longhorn about the new disk.** Skip this when the cluster does not run Longhorn. Longhorn remembers each node's disk by an identifier that was in the copy, and the new machine's disk has another, so Longhorn marks that disk `DiskFilesystemChanged`, puts no copies on it, and replicated volumes stay short of a copy. Look at the node:

   ```bash
   kubectl -n longhorn-system get nodes.longhorn.io <node name> -o jsonpath='{.spec.disks}{"\n"}{.status.diskStatus}'
   ```

   The disk's `Schedulable` condition is `False` with the reason `DiskNotReady`. Remove the disk and add it again. Longhorn only lets a disk go once it is disabled, and the new entry needs a name that is not the old one. Use the name that `.spec.disks` shows (such as `default-disk-7f80b132923582ce`), and the fields it shows for the new entry:

   ```bash
   kubectl -n longhorn-system patch nodes.longhorn.io <node name> --type merge -p '{"spec":{"disks":{"<disk name>":{"allowScheduling":false}}}}'
   kubectl -n longhorn-system patch nodes.longhorn.io <node name> --type merge -p '{"spec":{"disks":{"<disk name>":null}}}'
   kubectl -n longhorn-system patch nodes.longhorn.io <node name> --type merge -p '{"spec":{"disks":{"default-disk":{"allowScheduling":true,"diskType":"filesystem","path":"/var/lib/longhorn/","tags":[]}}}}'
   ```

   The disk is schedulable again within a minute, and the volumes that were short of a copy build it there. An instance of a highly available Postgres database whose volume was on the lost machine has lost its data with it: Geass replaces it by itself after it has been unable to start for [15 minutes](/docs/operations/high-availability/#what-geass-does-after-fifteen-minutes), and the Postgres operator builds a new one from the primary.

9. **Check what came back.** Open the dashboard. Projects, services, databases, domains and tokens are as they were. Workers that can reach the control plane at the address they joined, with the token they joined by, reconnect by themselves, which is how K3s agents behave; one that cannot needs its agent pointed at the new address. Whatever ran on the lost machine itself, and the volumes that were on its disk, are gone.

What this does not bring back: anything that changed after the copy was taken (up to 12 hours), the contents of local volumes that were on the lost machine, and anything that lives outside the cluster (DNS records, S3 buckets, your cloud accounts).

## Recover databases from a bucket

If the copies of the cluster are lost too, or you are moving to a new cluster and only have the bucket your database backups are in, the backups can still be found. Each database's backups are in a folder named for its project, and **beside them Geass keeps a small record of what they are copies of**: the engine, the version, the size, and what the database was called. It holds nothing secret, and an archive writes it a few minutes after a cluster runs this release. Backups made before that have no record until the cluster that made them has run it.

On the new cluster, open **Usage**, and **Recover databases from a bucket**. Give it the bucket, its region, and the AWS key that Geass used for backups (it makes a restricted user for the bucket from it, as it does for every project). It:

- finds each database's record, and its restore points from what is in the bucket;
- makes a project again, under the name the backups are filed under, for any that this cluster does not have, with a cloud connection to the bucket;
- makes a record of each database's backups, which then appears in that project's **Settings**, **Backups**, to restore as usual.

It changes nothing in the bucket and starts nothing. It lists what it left out, and why. Backups that were made before records were written, or whose record is of a newer release than yours, are left out.

The records it makes are offered for 30 days. **When they expire, only the records go: Geass never deletes the data in a bucket it found,** because the cluster that made it may be alive.

## Disk failures

The cluster tells you when a disk is failing, with the **Storage failing** [alert](/docs/guides/alerts/):

| Sent when | Means | After |
| --- | --- | --- |
| A node's disk has gone **read-only** | What the system does to a disk that is failing or a filesystem that is corrupted. Everything that writes to it fails | 2 minutes |
| A volume has **lost a copy** and not got it back | It still works, but another loss loses the data | 30 minutes |
| A volume has **lost every copy** | What uses it is down | 5 minutes |

A disk can fail quietly without any of this: a few bad sectors that nothing has read yet are not noticed by anything Geass watches. Replicated storage and backups are what protect against that, not detection.

## Limits

- A copy of the cluster holds its objects, not the contents of databases, volumes or in-cluster buckets. Databases need their own [backups](/docs/guides/backups-and-restore/).
- Redis has no backup, and the cluster's object storage server is not backed up.
- Geass replaces an instance of a highly available Postgres database when its node was lost, or its volume has no copy left. An instance whose volume is healthy but empty, because its data was removed from a disk that is still there, keeps crashing: Longhorn makes a new empty copy, and nothing tells that from a database that is slow to start. Replace it by hand by removing its claim and then its pod.
- Geass does not create machines, so replacing one starts with you adding it.
- The copies are as old as the last 00:00 or 12:00. Take one before something risky with `sudo k3s etcd-snapshot save`.
- A cluster that was not installed by Geass, or that uses another database than K3s's own etcd, cannot send copies this way.

## Related

- [High availability](/docs/operations/high-availability/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Upgrade Geass](/docs/operations/upgrading/#go-back)
- [Alerts](/docs/guides/alerts/)
- [Storage and volumes](/docs/guides/storage/)
